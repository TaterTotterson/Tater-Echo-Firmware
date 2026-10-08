/*
 * Small dlopen-only ONNX Runtime C bridge.
 *
 * Derived from EchoMuse's MIT-licensed on-device openWakeWord runtime.  ORT's
 * public C API is a table of function pointers, which cgo cannot call
 * directly.  Keeping the bridge header-only also means the Tater daemon has
 * no link-time dependency on the target-native runtime: MWW-only operation still
 * starts if the optional library is absent.
 */
#ifndef TATER_ORT_SHIM_H
#define TATER_ORT_SHIM_H

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "onnxruntime_c_api.h"

typedef const OrtApiBase *(*tater_ort_get_api_base_fn)(void);

typedef struct {
    void *dl;
    const OrtApi *api;
    OrtEnv *env;
} tater_ort_runtime;

typedef struct {
    const OrtApi *api;
    OrtSession *session;
    char *input_name;
    char *output_name;
    int xnnpack;
} tater_ort_model;

static char *tater_ort_dup(const char *text) {
    size_t size = strlen(text) + 1;
    char *copy = (char *)malloc(size);
    if (copy != NULL) memcpy(copy, text, size);
    return copy;
}

static char *tater_ort_error(const OrtApi *api, OrtStatus *status) {
    if (status == NULL) return NULL;
    char *message = tater_ort_dup(api->GetErrorMessage(status));
    api->ReleaseStatus(status);
    return message != NULL ? message : tater_ort_dup("onnxruntime: out of memory copying error");
}

static char *tater_ort_open(const char *path, tater_ort_runtime *out) {
    memset(out, 0, sizeof(*out));
    void *dl = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (dl == NULL) {
        const char *error = dlerror();
        return tater_ort_dup(error != NULL ? error : "dlopen failed");
    }
    tater_ort_get_api_base_fn get_base =
        (tater_ort_get_api_base_fn)dlsym(dl, "OrtGetApiBase");
    if (get_base == NULL) {
        const char *error = dlerror();
        char *message = tater_ort_dup(error != NULL ? error : "OrtGetApiBase not found");
        dlclose(dl);
        return message;
    }
    const OrtApiBase *base = get_base();
    if (base == NULL) {
        dlclose(dl);
        return tater_ort_dup("OrtGetApiBase returned NULL");
    }
    const OrtApi *api = base->GetApi(ORT_API_VERSION);
    if (api == NULL) {
        char message[192];
        snprintf(message, sizeof(message),
                 "onnxruntime does not provide C API version %d (library %s)",
                 ORT_API_VERSION, base->GetVersionString());
        dlclose(dl);
        return tater_ort_dup(message);
    }
    OrtEnv *env = NULL;
    OrtStatus *status = api->CreateEnv(ORT_LOGGING_LEVEL_ERROR, "tater-oww", &env);
    if (status != NULL) {
        char *message = tater_ort_error(api, status);
        dlclose(dl);
        return message;
    }
    out->dl = dl;
    out->api = api;
    out->env = env;
    return NULL;
}

static const char *tater_ort_version(tater_ort_runtime *runtime) {
    if (runtime->dl == NULL) return "";
    tater_ort_get_api_base_fn get_base =
        (tater_ort_get_api_base_fn)dlsym(runtime->dl, "OrtGetApiBase");
    return get_base != NULL ? get_base()->GetVersionString() : "";
}

static char *tater_ort_model_load(tater_ort_runtime *runtime, const char *path,
                                  tater_ort_model *out) {
    const OrtApi *api = runtime->api;
    if (api == NULL) return tater_ort_dup("onnxruntime is not open");
    memset(out, 0, sizeof(*out));
    out->api = api;

    OrtSessionOptions *options = NULL;
    char *error = tater_ort_error(api, api->CreateSessionOptions(&options));
    if (error != NULL) return error;

    /* EchoMuse measured this exact configuration on Biscuit: one thread,
     * XNNPACK, all graph optimizations, and no thread-pool spin waiting. */
    if ((error = tater_ort_error(api, api->SetIntraOpNumThreads(options, 1))) != NULL ||
        (error = tater_ort_error(api, api->SetSessionGraphOptimizationLevel(
            options, ORT_ENABLE_ALL))) != NULL) {
        api->ReleaseSessionOptions(options);
        return error;
    }
    const char *off = "0";
    if ((error = tater_ort_error(api, api->AddSessionConfigEntry(
             options, "session.intra_op.allow_spinning", off))) != NULL ||
        (error = tater_ort_error(api, api->AddSessionConfigEntry(
             options, "session.inter_op.allow_spinning", off))) != NULL) {
        api->ReleaseSessionOptions(options);
        return error;
    }

    const char *keys[] = {"intra_op_num_threads"};
    const char *values[] = {"1"};
    OrtStatus *provider_status = api->SessionOptionsAppendExecutionProvider(
        options, "XNNPACK", keys, values, 1);
    if (provider_status != NULL) {
        /* The CPU provider is slower but numerically equivalent. */
        api->ReleaseStatus(provider_status);
    } else {
        out->xnnpack = 1;
    }

    OrtSession *session = NULL;
    error = tater_ort_error(api, api->CreateSession(runtime->env, path, options, &session));
    api->ReleaseSessionOptions(options);
    if (error != NULL) return error;

    OrtAllocator *allocator = NULL;
    if ((error = tater_ort_error(api, api->GetAllocatorWithDefaultOptions(&allocator))) != NULL) {
        api->ReleaseSession(session);
        return error;
    }
    char *input_name = NULL;
    char *output_name = NULL;
    if ((error = tater_ort_error(api, api->SessionGetInputName(
             session, 0, allocator, &input_name))) != NULL ||
        (error = tater_ort_error(api, api->SessionGetOutputName(
             session, 0, allocator, &output_name))) != NULL) {
        api->ReleaseSession(session);
        return error;
    }
    out->session = session;
    out->input_name = input_name;
    out->output_name = output_name;
    return NULL;
}

static char *tater_ort_model_run(tater_ort_model *model,
                                 const float *input_data, size_t input_count,
                                 const int64_t *shape, size_t dimensions,
                                 float **output_data, size_t *output_count) {
    const OrtApi *api = model->api;
    if (api == NULL || model->session == NULL) return tater_ort_dup("model is closed");
    *output_data = NULL;
    *output_count = 0;

    OrtMemoryInfo *memory = NULL;
    char *error = tater_ort_error(api, api->CreateCpuMemoryInfo(
        OrtArenaAllocator, OrtMemTypeDefault, &memory));
    if (error != NULL) return error;

    OrtValue *input = NULL;
    OrtValue *output = NULL;
    error = tater_ort_error(api, api->CreateTensorWithDataAsOrtValue(
        memory, (void *)input_data, input_count * sizeof(float), shape, dimensions,
        ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, &input));
    api->ReleaseMemoryInfo(memory);
    if (error != NULL) return error;

    error = tater_ort_error(api, api->Run(
        model->session, NULL,
        (const char *const *)&model->input_name, (const OrtValue *const *)&input, 1,
        (const char *const *)&model->output_name, 1, &output));
    api->ReleaseValue(input);
    if (error != NULL) return error;

    OrtTensorTypeAndShapeInfo *tensor_info = NULL;
    if ((error = tater_ort_error(api, api->GetTensorTypeAndShape(
             output, &tensor_info))) != NULL) {
        api->ReleaseValue(output);
        return error;
    }
    size_t count = 0;
    error = tater_ort_error(api, api->GetTensorShapeElementCount(tensor_info, &count));
    api->ReleaseTensorTypeAndShapeInfo(tensor_info);
    if (error != NULL) {
        api->ReleaseValue(output);
        return error;
    }

    float *source = NULL;
    if ((error = tater_ort_error(api, api->GetTensorMutableData(
             output, (void **)&source))) != NULL) {
        api->ReleaseValue(output);
        return error;
    }
    float *copy = (float *)malloc(count * sizeof(float));
    if (copy == NULL) {
        api->ReleaseValue(output);
        return tater_ort_dup("out of memory copying model output");
    }
    memcpy(copy, source, count * sizeof(float));
    api->ReleaseValue(output);
    *output_data = copy;
    *output_count = count;
    return NULL;
}

static void tater_ort_model_free(tater_ort_model *model) {
    const OrtApi *api = model->api;
    if (api == NULL || model->session == NULL) return;
    api->ReleaseSession(model->session);
    model->session = NULL;

    OrtAllocator *allocator = NULL;
    OrtStatus *status = api->GetAllocatorWithDefaultOptions(&allocator);
    if (status != NULL) {
        api->ReleaseStatus(status);
        return;
    }
    if (model->input_name != NULL) {
        status = api->AllocatorFree(allocator, model->input_name);
        if (status != NULL) api->ReleaseStatus(status);
        model->input_name = NULL;
    }
    if (model->output_name != NULL) {
        status = api->AllocatorFree(allocator, model->output_name);
        if (status != NULL) api->ReleaseStatus(status);
        model->output_name = NULL;
    }
}

#endif
