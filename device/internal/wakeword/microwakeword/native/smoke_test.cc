// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#include "c_api.h"

#include <cmath>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <iterator>
#include <string>
#include <vector>

namespace {

bool ProcessZeros(void* engine, std::vector<float>* scores) {
  std::vector<std::int16_t> pcm(32000, 0);
  scores->assign(256, 0.0f);
  std::size_t count = 0;
  char* error = nullptr;
  const int ok = tater_mww_engine_process(
      engine, pcm.data(), pcm.size(), scores->data(), scores->size(), &count,
      &error);
  if (!ok) {
    std::cerr << "process failed: " << (error != nullptr ? error : "unknown")
              << '\n';
    tater_mww_free_error(error);
    return false;
  }
  scores->resize(count);
  for (float score : *scores) {
    if (!std::isfinite(score) || score < 0.0f || score > 1.0f) {
      std::cerr << "invalid score " << score << '\n';
      return false;
    }
  }
  return !scores->empty();
}

}  // namespace

int main(int argc, char** argv) {
  if (argc != 2) {
    std::cerr << "usage: tater_mww_smoke MODEL.tflite\n";
    return 2;
  }
  std::ifstream stream(argv[1], std::ios::binary);
  if (!stream) {
    std::cerr << "cannot open model " << argv[1] << '\n';
    return 2;
  }
  std::vector<std::uint8_t> model((std::istreambuf_iterator<char>(stream)),
                                  std::istreambuf_iterator<char>());
  tater_mww_config config{};
  config.sample_rate = 16000;
  config.feature_duration_ms = 30;
  config.feature_step_ms = 10;
  config.feature_size = 40;
  config.input_feature_frames = 2;
  config.lower_band_limit = 125.0f;
  config.upper_band_limit = 7500.0f;
  config.tensor_arena_size = 192 * 1024;

  char* error = nullptr;
  void* engine = tater_mww_engine_create(model.data(), model.size(), &config,
                                         &error);
  if (engine == nullptr) {
    std::cerr << "create failed: " << (error != nullptr ? error : "unknown")
              << '\n';
    tater_mww_free_error(error);
    return 1;
  }
  std::cout << tater_mww_runtime_version() << "; "
            << tater_mww_engine_info(engine) << '\n';

  std::vector<float> first;
  std::vector<float> second;
  if (!ProcessZeros(engine, &first)) {
    tater_mww_engine_destroy(engine);
    return 1;
  }
  if (!tater_mww_engine_reset(engine, &error)) {
    std::cerr << "reset failed: " << (error != nullptr ? error : "unknown")
              << '\n';
    tater_mww_free_error(error);
    tater_mww_engine_destroy(engine);
    return 1;
  }
  if (!ProcessZeros(engine, &second) || first != second) {
    std::cerr << "reset did not reproduce the initial score sequence\n";
    tater_mww_engine_destroy(engine);
    return 1;
  }

  std::cout << "scores=" << first.size() << " first=" << first.front()
            << " last=" << first.back()
            << " arena_used=" << tater_mww_engine_arena_used(engine)
            << " stride=" << tater_mww_engine_input_stride(engine) << '\n';
  tater_mww_engine_destroy(engine);
  return 0;
}
