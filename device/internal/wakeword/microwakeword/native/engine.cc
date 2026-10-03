// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#include "engine.h"

#include <algorithm>
#include <cmath>
#include <cstring>
#include <limits>
#include <sstream>

#include "flatbuffers/flatbuffers.h"
#include "tensorflow/lite/micro/micro_allocator.h"
#include "tensorflow/lite/micro/micro_resource_variable.h"
#include "tensorflow/lite/schema/schema_generated.h"

extern "C" {
#include "tensorflow/lite/experimental/microfrontend/lib/frontend_util.h"
}

namespace tater::microwakeword {
namespace {

constexpr std::size_t kMinModelBytes = 64;
constexpr std::size_t kMaxModelBytes = 2 * 1024 * 1024;
constexpr std::size_t kMinTensorArenaBytes = 16 * 1024;
constexpr std::size_t kMaxTensorArenaBytes = 4 * 1024 * 1024;
constexpr int kResourceVariableSlots = 20;
constexpr float kFrontendFloatScale = 0.0390625f;

constexpr int kNoiseReductionSmoothingBits = 10;
constexpr float kNoiseReductionEvenSmoothing = 0.025f;
constexpr float kNoiseReductionOddSmoothing = 0.06f;
constexpr float kNoiseReductionMinSignalRemaining = 0.05f;
constexpr float kPcanStrength = 0.95f;
constexpr float kPcanOffset = 80.0f;
constexpr int kPcanGainBits = 21;
constexpr int kLogScaleShift = 6;

bool Status(TfLiteStatus status, const char* operation, std::string* error) {
  if (status == kTfLiteOk) {
    return true;
  }
  if (error != nullptr) {
    *error = std::string(operation) + " failed";
  }
  return false;
}

}  // namespace

Engine::Engine(const EngineConfig& config) : config_(config) {}

Engine::~Engine() {
  interpreter_.reset();
  if (frontend_initialized_) {
    FrontendFreeStateContents(&frontend_state_);
  }
}

std::unique_ptr<Engine> Engine::Create(const std::uint8_t* model_data,
                                       std::size_t model_size,
                                       const EngineConfig& config,
                                       std::string* error) {
  auto engine = std::unique_ptr<Engine>(new Engine(config));
  if (!engine->Initialize(model_data, model_size, error)) {
    return nullptr;
  }
  return engine;
}

bool Engine::Initialize(const std::uint8_t* model_data, std::size_t model_size,
                        std::string* error) {
  if (model_data == nullptr || model_size < kMinModelBytes ||
      model_size > kMaxModelBytes) {
    if (error != nullptr) {
      *error = "model size must be between 64 bytes and 2 MiB";
    }
    return false;
  }
  if (config_.sample_rate != 16000 || config_.feature_duration_ms != 30 ||
      config_.feature_step_ms != 10 || config_.feature_size != 40 ||
      config_.input_feature_frames != 2) {
    if (error != nullptr) {
      *error = "unsupported frontend shape; expected 16 kHz, 30/10 ms, 40 bins, 2 input frames";
    }
    return false;
  }
  if (config_.lower_band_limit <= 0.0f ||
      config_.upper_band_limit <= config_.lower_band_limit ||
      config_.upper_band_limit > static_cast<float>(config_.sample_rate) / 2.0f) {
    if (error != nullptr) {
      *error = "invalid frontend band limits";
    }
    return false;
  }
  if (config_.tensor_arena_size < kMinTensorArenaBytes ||
      config_.tensor_arena_size > kMaxTensorArenaBytes) {
    if (error != nullptr) {
      *error = "tensor arena must be between 16 KiB and 4 MiB";
    }
    return false;
  }
  if (!InitializeFrontend(error) || !RegisterOps(error)) {
    return false;
  }

  model_copy_ = std::make_unique<std::uint8_t[]>(model_size);
  std::memcpy(model_copy_.get(), model_data, model_size);
  model_size_ = model_size;

  flatbuffers::Verifier verifier(model_copy_.get(), model_size_);
  if (!tflite::VerifyModelBuffer(verifier)) {
    if (error != nullptr) {
      *error = "model is not a valid TFLite flatbuffer";
    }
    return false;
  }
  const tflite::Model* model = tflite::GetModel(model_copy_.get());
  if (model == nullptr || model->version() != TFLITE_SCHEMA_VERSION) {
    if (error != nullptr) {
      *error = "unsupported TFLite schema version";
    }
    return false;
  }

  auto* allocator =
      tflite::MicroAllocator::Create(variable_arena_.data(), variable_arena_.size());
  if (allocator == nullptr) {
    if (error != nullptr) {
      *error = "could not create TFLM variable allocator";
    }
    return false;
  }
  auto* variables =
      tflite::MicroResourceVariables::Create(allocator, kResourceVariableSlots);
  if (variables == nullptr) {
    if (error != nullptr) {
      *error = "could not create TFLM resource variables";
    }
    return false;
  }

  tensor_arena_.resize(config_.tensor_arena_size);
  interpreter_ = std::make_unique<tflite::MicroInterpreter>(
      model, op_resolver_, tensor_arena_.data(), tensor_arena_.size(), variables);
  if (!Status(interpreter_->AllocateTensors(), "AllocateTensors", error)) {
    interpreter_.reset();
    return false;
  }
  arena_used_bytes_ = interpreter_->arena_used_bytes();

  TfLiteTensor* input = interpreter_->input(0);
  if (input == nullptr || input->dims == nullptr || input->dims->size != 3 ||
      input->dims->data[0] != 1 ||
      input->dims->data[1] != config_.input_feature_frames ||
      input->dims->data[2] != config_.feature_size || input->type != kTfLiteInt8) {
    if (error != nullptr) {
      *error = "model input must be int8 [1,2,40]";
    }
    return false;
  }
  if (input->params.scale <= 0.0f) {
    if (error != nullptr) {
      *error = "model input has invalid quantization scale";
    }
    return false;
  }
  input_stride_ = input->dims->data[1];
  input_scale_ = input->params.scale;
  input_zero_point_ = input->params.zero_point;

  TfLiteTensor* output = interpreter_->output(0);
  if (output == nullptr || output->dims == nullptr || output->dims->size != 2 ||
      output->dims->data[0] != 1 || output->dims->data[1] != 1 ||
      output->type != kTfLiteUInt8 || output->bytes < 1) {
    if (error != nullptr) {
      *error = "model output must be uint8 [1,1]";
    }
    return false;
  }

  std::ostringstream description;
  description << "TFLite Micro 2747abd, stride=" << input_stride_
              << ", arena=" << arena_used_bytes_ << "/"
              << tensor_arena_.size() << " bytes";
  info_ = description.str();
  return true;
}

bool Engine::InitializeFrontend(std::string* error) {
  FrontendConfig frontend_config{};
  FrontendFillConfigWithDefaults(&frontend_config);
  frontend_config.window.size_ms = config_.feature_duration_ms;
  frontend_config.window.step_size_ms = config_.feature_step_ms;
  frontend_config.filterbank.num_channels = config_.feature_size;
  frontend_config.filterbank.lower_band_limit = config_.lower_band_limit;
  frontend_config.filterbank.upper_band_limit = config_.upper_band_limit;
  frontend_config.noise_reduction.smoothing_bits = kNoiseReductionSmoothingBits;
  frontend_config.noise_reduction.even_smoothing =
      kNoiseReductionEvenSmoothing;
  frontend_config.noise_reduction.odd_smoothing = kNoiseReductionOddSmoothing;
  frontend_config.noise_reduction.min_signal_remaining =
      kNoiseReductionMinSignalRemaining;
  frontend_config.pcan_gain_control.enable_pcan = 1;
  frontend_config.pcan_gain_control.strength = kPcanStrength;
  frontend_config.pcan_gain_control.offset = kPcanOffset;
  frontend_config.pcan_gain_control.gain_bits = kPcanGainBits;
  frontend_config.log_scale.enable_log = 1;
  frontend_config.log_scale.scale_shift = kLogScaleShift;

  if (!FrontendPopulateState(&frontend_config, &frontend_state_,
                             config_.sample_rate)) {
    if (error != nullptr) {
      *error = "could not initialize TFLM microfrontend";
    }
    return false;
  }
  frontend_initialized_ = true;
  return true;
}

bool Engine::RegisterOps(std::string* error) {
#define TATER_ADD_OP(call)                 \
  do {                                     \
    if (!Status(op_resolver_.call, #call, error)) { \
      return false;                        \
    }                                      \
  } while (false)

  TATER_ADD_OP(AddCallOnce());
  TATER_ADD_OP(AddVarHandle());
  TATER_ADD_OP(AddReshape());
  TATER_ADD_OP(AddReadVariable());
  TATER_ADD_OP(AddStridedSlice());
  TATER_ADD_OP(AddConcatenation());
  TATER_ADD_OP(AddAssignVariable());
  TATER_ADD_OP(AddConv2D());
  TATER_ADD_OP(AddMul());
  TATER_ADD_OP(AddAdd());
  TATER_ADD_OP(AddMean());
  TATER_ADD_OP(AddFullyConnected());
  TATER_ADD_OP(AddLogistic());
  TATER_ADD_OP(AddQuantize());
  TATER_ADD_OP(AddDepthwiseConv2D());
  TATER_ADD_OP(AddAveragePool2D());
  TATER_ADD_OP(AddMaxPool2D());
  TATER_ADD_OP(AddPad());
  TATER_ADD_OP(AddPack());
  TATER_ADD_OP(AddSplitV());

#undef TATER_ADD_OP
  return true;
}

bool Engine::Process(const std::int16_t* samples, std::size_t sample_count,
                     float* scores, std::size_t score_capacity,
                     std::size_t* score_count, std::string* error) {
  if (score_count == nullptr) {
    if (error != nullptr) {
      *error = "score_count is required";
    }
    return false;
  }
  *score_count = 0;
  if (sample_count == 0) {
    return true;
  }
  if (samples == nullptr || scores == nullptr || score_capacity == 0) {
    if (error != nullptr) {
      *error = "samples and score buffer are required";
    }
    return false;
  }

  std::size_t offset = 0;
  while (offset < sample_count) {
    std::size_t consumed = 0;
    FrontendOutput output = FrontendProcessSamples(
        &frontend_state_, samples + offset, sample_count - offset, &consumed);
    if (consumed == 0 && output.size == 0) {
      break;
    }
    offset += consumed;
    if (output.values == nullptr || output.size == 0) {
      continue;
    }
    if (!ProcessFeature(output.values, output.size, scores, score_capacity,
                        score_count, error)) {
      return false;
    }
  }
  return true;
}

bool Engine::ProcessFeature(const std::uint16_t* values,
                            std::size_t value_count, float* scores,
                            std::size_t score_capacity,
                            std::size_t* score_count, std::string* error) {
  if (value_count != static_cast<std::size_t>(config_.feature_size)) {
    if (error != nullptr) {
      *error = "microfrontend returned an unexpected feature count";
    }
    return false;
  }
  if (feature_observer_ != nullptr) {
    feature_observer_(values, value_count, feature_observer_context_);
  }
  TfLiteTensor* input = interpreter_->input(0);
  if (input == nullptr || input->data.int8 == nullptr) {
    if (error != nullptr) {
      *error = "model input tensor is unavailable";
    }
    return false;
  }

  std::int8_t* destination =
      input->data.int8 + (current_stride_step_ * config_.feature_size);
  for (int i = 0; i < config_.feature_size; ++i) {
    const float feature = static_cast<float>(values[i]) * kFrontendFloatScale;
    const long quantized = std::lround(
        feature / input_scale_ + static_cast<float>(input_zero_point_));
    destination[i] = static_cast<std::int8_t>(std::clamp<long>(
        quantized, std::numeric_limits<std::int8_t>::min(),
        std::numeric_limits<std::int8_t>::max()));
  }

  ++current_stride_step_;
  if (current_stride_step_ < input_stride_) {
    return true;
  }
  current_stride_step_ = 0;
  if (*score_count >= score_capacity) {
    if (error != nullptr) {
      *error = "score buffer is too small for the supplied PCM chunk";
    }
    return false;
  }
  float score = 0.0f;
  if (!Invoke(&score, error)) {
    return false;
  }
  scores[(*score_count)++] = score;
  return true;
}

bool Engine::ProcessFeatureForDiagnostics(const std::uint16_t* values,
                                          std::size_t value_count,
                                          float* score,
                                          bool* produced_score,
                                          std::string* error) {
  if (score == nullptr || produced_score == nullptr) {
    if (error != nullptr) {
      *error = "diagnostic score outputs are required";
    }
    return false;
  }
  std::size_t score_count = 0;
  *score = 0.0f;
  *produced_score = false;
  if (!ProcessFeature(values, value_count, score, 1, &score_count, error)) {
    return false;
  }
  *produced_score = score_count == 1;
  return true;
}

bool Engine::Invoke(float* score, std::string* error) {
  if (!Status(interpreter_->Invoke(), "Invoke", error)) {
    return false;
  }
  invoked_ = true;
  const TfLiteTensor* output = interpreter_->output(0);
  if (output == nullptr || output->data.uint8 == nullptr) {
    if (error != nullptr) {
      *error = "model output tensor is unavailable after Invoke";
    }
    return false;
  }
  *score = static_cast<float>(output->data.uint8[0]) / 255.0f;
  return true;
}

bool Engine::Reset(std::string* error) {
  if (frontend_initialized_) {
    FrontendReset(&frontend_state_);
  }
  current_stride_step_ = 0;
  if (TfLiteTensor* input = interpreter_ != nullptr ? interpreter_->input(0)
                                                    : nullptr;
      input != nullptr && input->data.raw != nullptr) {
    std::memset(input->data.raw, 0, input->bytes);
  }
  if (invoked_ && !Status(interpreter_->Reset(), "Reset", error)) {
    return false;
  }
  invoked_ = false;
  return true;
}

}  // namespace tater::microwakeword
