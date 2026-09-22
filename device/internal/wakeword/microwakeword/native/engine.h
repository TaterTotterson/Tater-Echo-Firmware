// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "tensorflow/lite/experimental/microfrontend/lib/frontend.h"
#include "tensorflow/lite/micro/micro_interpreter.h"
#include "tensorflow/lite/micro/micro_mutable_op_resolver.h"

namespace tater::microwakeword {

struct EngineConfig {
  int sample_rate = 16000;
  int feature_duration_ms = 30;
  int feature_step_ms = 10;
  int feature_size = 40;
  int input_feature_frames = 2;
  float lower_band_limit = 125.0f;
  float upper_band_limit = 7500.0f;
  std::size_t tensor_arena_size = 64 * 1024;
};

// Engine owns one TFLite Micro interpreter and microfrontend state. It is
// single-threaded by design; the Go layer runs it from a dedicated inference
// goroutine so microphone capture never waits on inference.
class Engine {
 public:
  using FeatureObserver = void (*)(const std::uint16_t* values,
                                   std::size_t value_count, void* context);

  static std::unique_ptr<Engine> Create(const std::uint8_t* model_data,
                                        std::size_t model_size,
                                        const EngineConfig& config,
                                        std::string* error);

  ~Engine();

  Engine(const Engine&) = delete;
  Engine& operator=(const Engine&) = delete;
  Engine(Engine&&) = delete;
  Engine& operator=(Engine&&) = delete;

  // Process accepts arbitrary-sized 16-bit mono PCM chunks and appends each
  // model score to scores. score_capacity must be large enough for every
  // complete stride produced by the chunk.
  bool Process(const std::int16_t* samples, std::size_t sample_count,
               float* scores, std::size_t score_capacity,
               std::size_t* score_count, std::string* error);

  // Reset clears frontend overlap, adaptive frontend state, input stride, and
  // all TFLM resource variables. The resource-variable reset is essential for
  // stateful streaming models after a microphone discontinuity.
  bool Reset(std::string* error);

  // Installs a raw microfrontend observer for host diagnostics and golden
  // parity tests. This is intentionally not part of the stable C ABI.
  void SetFeatureObserver(FeatureObserver observer, void* context) {
    feature_observer_ = observer;
    feature_observer_context_ = context;
  }

  // Feeds one already-extracted uint16 feature frame through the production
  // quantization and model path. Host golden tests use this to distinguish
  // frontend drift from TFLM inference drift. No stable C ABI exposes it.
  bool ProcessFeatureForDiagnostics(const std::uint16_t* values,
                                    std::size_t value_count, float* score,
                                    bool* produced_score, std::string* error);

  const std::string& info() const { return info_; }
  std::size_t arena_used_bytes() const { return arena_used_bytes_; }
  int input_stride() const { return input_stride_; }

 private:
  using OpResolver = tflite::MicroMutableOpResolver<20>;

  explicit Engine(const EngineConfig& config);

  bool Initialize(const std::uint8_t* model_data, std::size_t model_size,
                  std::string* error);
  bool InitializeFrontend(std::string* error);
  bool RegisterOps(std::string* error);
  bool ProcessFeature(const std::uint16_t* values, std::size_t value_count,
                      float* scores, std::size_t score_capacity,
                      std::size_t* score_count, std::string* error);
  bool Invoke(float* score, std::string* error);

  EngineConfig config_;
  FrontendState frontend_state_{};
  bool frontend_initialized_ = false;

  std::unique_ptr<std::uint8_t[]> model_copy_;
  std::size_t model_size_ = 0;
  std::vector<std::uint8_t> tensor_arena_;
  alignas(16) std::array<std::uint8_t, 4096> variable_arena_{};
  OpResolver op_resolver_;
  std::unique_ptr<tflite::MicroInterpreter> interpreter_;

  int input_stride_ = 0;
  int current_stride_step_ = 0;
  float input_scale_ = 0.0f;
  int input_zero_point_ = 0;
  std::size_t arena_used_bytes_ = 0;
  bool invoked_ = false;
  FeatureObserver feature_observer_ = nullptr;
  void* feature_observer_context_ = nullptr;
  std::string info_;
};

}  // namespace tater::microwakeword
