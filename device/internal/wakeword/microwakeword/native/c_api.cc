// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#include "c_api.h"

#include <cstdlib>
#include <cstring>
#include <exception>
#include <memory>
#include <string>

#include "engine.h"

namespace {

using NativeEngine = tater::microwakeword::Engine;

struct EngineHandle {
  std::unique_ptr<NativeEngine> engine;
};

void SetError(char** error_out, const std::string& message) {
  if (error_out == nullptr) {
    return;
  }
  *error_out = nullptr;
  char* copy = static_cast<char*>(std::malloc(message.size() + 1));
  if (copy != nullptr) {
    std::memcpy(copy, message.c_str(), message.size() + 1);
    *error_out = copy;
  }
}

void ClearError(char** error_out) {
  if (error_out != nullptr) {
    *error_out = nullptr;
  }
}

}  // namespace

extern "C" {

uint32_t tater_mww_abi_version(void) { return TATER_MWW_ABI_VERSION; }

const char* tater_mww_runtime_version(void) {
  return "1.0.0+tflm.2747abd";
}

void* tater_mww_engine_create(const uint8_t* model_data, size_t model_size,
                              const tater_mww_config* config,
                              char** error_out) {
  ClearError(error_out);
  if (config == nullptr) {
    SetError(error_out, "engine config is required");
    return nullptr;
  }
  try {
    tater::microwakeword::EngineConfig native_config;
    native_config.sample_rate = config->sample_rate;
    native_config.feature_duration_ms = config->feature_duration_ms;
    native_config.feature_step_ms = config->feature_step_ms;
    native_config.feature_size = config->feature_size;
    native_config.input_feature_frames = config->input_feature_frames;
    native_config.lower_band_limit = config->lower_band_limit;
    native_config.upper_band_limit = config->upper_band_limit;
    native_config.tensor_arena_size = config->tensor_arena_size;

    std::string error;
    auto engine = NativeEngine::Create(model_data, model_size, native_config,
                                       &error);
    if (engine == nullptr) {
      SetError(error_out, error.empty() ? "engine initialization failed" : error);
      return nullptr;
    }
    auto handle = std::make_unique<EngineHandle>();
    handle->engine = std::move(engine);
    return handle.release();
  } catch (const std::exception& exception) {
    SetError(error_out, std::string("engine initialization exception: ") +
                            exception.what());
    return nullptr;
  } catch (...) {
    SetError(error_out, "engine initialization exception");
    return nullptr;
  }
}

int tater_mww_engine_process(void* engine, const int16_t* samples,
                             size_t sample_count, float* scores,
                             size_t score_capacity, size_t* score_count,
                             char** error_out) {
  ClearError(error_out);
  auto* handle = static_cast<EngineHandle*>(engine);
  if (handle == nullptr || handle->engine == nullptr) {
    SetError(error_out, "engine is closed");
    return 0;
  }
  std::string error;
  if (!handle->engine->Process(samples, sample_count, scores, score_capacity,
                               score_count, &error)) {
    SetError(error_out, error.empty() ? "audio processing failed" : error);
    return 0;
  }
  return 1;
}

int tater_mww_engine_reset(void* engine, char** error_out) {
  ClearError(error_out);
  auto* handle = static_cast<EngineHandle*>(engine);
  if (handle == nullptr || handle->engine == nullptr) {
    SetError(error_out, "engine is closed");
    return 0;
  }
  std::string error;
  if (!handle->engine->Reset(&error)) {
    SetError(error_out, error.empty() ? "engine reset failed" : error);
    return 0;
  }
  return 1;
}

const char* tater_mww_engine_info(void* engine) {
  auto* handle = static_cast<EngineHandle*>(engine);
  if (handle == nullptr || handle->engine == nullptr) {
    return "";
  }
  return handle->engine->info().c_str();
}

size_t tater_mww_engine_arena_used(void* engine) {
  auto* handle = static_cast<EngineHandle*>(engine);
  return handle != nullptr && handle->engine != nullptr
             ? handle->engine->arena_used_bytes()
             : 0;
}

int32_t tater_mww_engine_input_stride(void* engine) {
  auto* handle = static_cast<EngineHandle*>(engine);
  return handle != nullptr && handle->engine != nullptr
             ? handle->engine->input_stride()
             : 0;
}

void tater_mww_engine_destroy(void* engine) {
  delete static_cast<EngineHandle*>(engine);
}

void tater_mww_free_error(char* error) { std::free(error); }

}  // extern "C"
