/*
 * The firmware loads libtater_microwakeword at runtime instead of linking it.
 * A missing or incompatible inference payload must disable local wake, never
 * prevent the satellite process from booting. This shim also turns C function
 * pointers into ordinary functions cgo can call.
 */
#ifndef TATER_MWW_NATIVE_SHIM_H
#define TATER_MWW_NATIVE_SHIM_H

#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "native/c_api.h"

typedef uint32_t (*em_mww_abi_fn)(void);
typedef const char *(*em_mww_version_fn)(void);
typedef void *(*em_mww_create_fn)(const uint8_t *, size_t,
                                  const tater_mww_config *, char **);
typedef int (*em_mww_process_fn)(void *, const int16_t *, size_t, float *,
                                 size_t, size_t *, char **);
typedef int (*em_mww_reset_fn)(void *, char **);
typedef const char *(*em_mww_info_fn)(void *);
typedef size_t (*em_mww_arena_fn)(void *);
typedef int32_t (*em_mww_stride_fn)(void *);
typedef void (*em_mww_destroy_fn)(void *);
typedef void (*em_mww_free_error_fn)(char *);

typedef struct {
  void *dl;
  em_mww_version_fn version;
  em_mww_create_fn create;
  em_mww_process_fn process;
  em_mww_reset_fn reset;
  em_mww_info_fn info;
  em_mww_arena_fn arena_used;
  em_mww_stride_fn input_stride;
  em_mww_destroy_fn destroy;
  em_mww_free_error_fn free_error;
} em_mww_runtime;

static char *em_mww_dup(const char *message) {
  if (message == NULL) message = "unknown native runtime error";
  size_t length = strlen(message) + 1;
  char *copy = (char *)malloc(length);
  if (copy != NULL) memcpy(copy, message, length);
  return copy;
}

static char *em_mww_dlerror(const char *prefix) {
  const char *detail = dlerror();
  char message[512];
  snprintf(message, sizeof(message), "%s: %s", prefix,
           detail != NULL ? detail : "symbol not found");
  return em_mww_dup(message);
}

#define EM_MWW_RESOLVE(field, type, symbol)                         \
  do {                                                               \
    dlerror();                                                        \
    out->field = (type)dlsym(dl, symbol);                             \
    if (out->field == NULL) {                                         \
      char *error = em_mww_dlerror("microwakeword runtime missing " symbol); \
      dlclose(dl);                                                     \
      memset(out, 0, sizeof(*out));                                   \
      return error;                                                    \
    }                                                                 \
  } while (0)

static char *em_mww_open(const char *path, em_mww_runtime *out) {
  memset(out, 0, sizeof(*out));
  void *dl = dlopen(path, RTLD_NOW | RTLD_LOCAL);
  if (dl == NULL) return em_mww_dlerror("open microwakeword runtime");
  out->dl = dl;

  em_mww_abi_fn abi;
  dlerror();
  abi = (em_mww_abi_fn)dlsym(dl, "tater_mww_abi_version");
  if (abi == NULL) {
    char *error = em_mww_dlerror("microwakeword runtime missing ABI symbol");
    dlclose(dl);
    memset(out, 0, sizeof(*out));
    return error;
  }
  if (abi() != TATER_MWW_ABI_VERSION) {
    char message[160];
    snprintf(message, sizeof(message),
             "microwakeword runtime ABI %u is incompatible; firmware expects %u",
             (unsigned)abi(), (unsigned)TATER_MWW_ABI_VERSION);
    dlclose(dl);
    memset(out, 0, sizeof(*out));
    return em_mww_dup(message);
  }

  EM_MWW_RESOLVE(version, em_mww_version_fn, "tater_mww_runtime_version");
  EM_MWW_RESOLVE(create, em_mww_create_fn, "tater_mww_engine_create");
  EM_MWW_RESOLVE(process, em_mww_process_fn, "tater_mww_engine_process");
  EM_MWW_RESOLVE(reset, em_mww_reset_fn, "tater_mww_engine_reset");
  EM_MWW_RESOLVE(info, em_mww_info_fn, "tater_mww_engine_info");
  EM_MWW_RESOLVE(arena_used, em_mww_arena_fn, "tater_mww_engine_arena_used");
  EM_MWW_RESOLVE(input_stride, em_mww_stride_fn,
                 "tater_mww_engine_input_stride");
  EM_MWW_RESOLVE(destroy, em_mww_destroy_fn, "tater_mww_engine_destroy");
  EM_MWW_RESOLVE(free_error, em_mww_free_error_fn,
                 "tater_mww_free_error");
  return NULL;
}

#undef EM_MWW_RESOLVE

static char *em_mww_take_error(em_mww_runtime *runtime,
                               char *library_error) {
  if (library_error == NULL) return NULL;
  char *copy = em_mww_dup(library_error);
  runtime->free_error(library_error);
  return copy;
}

static char *em_mww_create(em_mww_runtime *runtime, const uint8_t *model,
                           size_t model_size,
                           const tater_mww_config *config, void **out) {
  char *library_error = NULL;
  *out = runtime->create(model, model_size, config, &library_error);
  if (*out == NULL) {
    char *error = em_mww_take_error(runtime, library_error);
    return error != NULL ? error : em_mww_dup("native engine creation failed");
  }
  if (library_error != NULL) runtime->free_error(library_error);
  return NULL;
}

static char *em_mww_process(em_mww_runtime *runtime, void *engine,
                            const int16_t *samples, size_t sample_count,
                            float *scores, size_t score_capacity,
                            size_t *score_count) {
  char *library_error = NULL;
  if (!runtime->process(engine, samples, sample_count, scores, score_capacity,
                        score_count, &library_error)) {
    char *error = em_mww_take_error(runtime, library_error);
    return error != NULL ? error : em_mww_dup("native audio processing failed");
  }
  if (library_error != NULL) runtime->free_error(library_error);
  return NULL;
}

static char *em_mww_reset(em_mww_runtime *runtime, void *engine) {
  char *library_error = NULL;
  if (!runtime->reset(engine, &library_error)) {
    char *error = em_mww_take_error(runtime, library_error);
    return error != NULL ? error : em_mww_dup("native engine reset failed");
  }
  if (library_error != NULL) runtime->free_error(library_error);
  return NULL;
}

static const char *em_mww_version(em_mww_runtime *runtime) {
  return runtime->version();
}

static const char *em_mww_info(em_mww_runtime *runtime, void *engine) {
  return runtime->info(engine);
}

static size_t em_mww_arena_used(em_mww_runtime *runtime, void *engine) {
  return runtime->arena_used(engine);
}

static int32_t em_mww_input_stride(em_mww_runtime *runtime, void *engine) {
  return runtime->input_stride(engine);
}

static void em_mww_destroy(em_mww_runtime *runtime, void *engine) {
  runtime->destroy(engine);
}

#endif
