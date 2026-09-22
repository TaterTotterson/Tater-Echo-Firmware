// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#pragma once

#include <stddef.h>
#include <stdint.h>

#if defined(_WIN32)
#define TATER_MWW_EXPORT __declspec(dllexport)
#else
#define TATER_MWW_EXPORT __attribute__((visibility("default")))
#endif

#ifdef __cplusplus
extern "C" {
#endif

#define TATER_MWW_ABI_VERSION 1u

typedef struct tater_mww_config {
  int32_t sample_rate;
  int32_t feature_duration_ms;
  int32_t feature_step_ms;
  int32_t feature_size;
  int32_t input_feature_frames;
  float lower_band_limit;
  float upper_band_limit;
  size_t tensor_arena_size;
} tater_mww_config;

TATER_MWW_EXPORT uint32_t tater_mww_abi_version(void);
TATER_MWW_EXPORT const char* tater_mww_runtime_version(void);

// Errors are returned through error_out as library-owned malloc strings. The
// caller releases them with tater_mww_free_error.
TATER_MWW_EXPORT void* tater_mww_engine_create(
    const uint8_t* model_data, size_t model_size,
    const tater_mww_config* config, char** error_out);
TATER_MWW_EXPORT int tater_mww_engine_process(
    void* engine, const int16_t* samples, size_t sample_count, float* scores,
    size_t score_capacity, size_t* score_count, char** error_out);
TATER_MWW_EXPORT int tater_mww_engine_reset(void* engine, char** error_out);
TATER_MWW_EXPORT const char* tater_mww_engine_info(void* engine);
TATER_MWW_EXPORT size_t tater_mww_engine_arena_used(void* engine);
TATER_MWW_EXPORT int32_t tater_mww_engine_input_stride(void* engine);
TATER_MWW_EXPORT void tater_mww_engine_destroy(void* engine);
TATER_MWW_EXPORT void tater_mww_free_error(char* error);

#ifdef __cplusplus
}
#endif
