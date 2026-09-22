// Copyright 2026 Tater Assistant contributors.
// SPDX-License-Identifier: MIT

#include "engine.h"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstdint>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <iterator>
#include <limits>
#include <string>
#include <vector>

namespace {

constexpr std::size_t kFixtureSamples = 32000;
constexpr std::size_t kFeatureSize = 40;
constexpr std::size_t kModelProbeFrames = 400;
constexpr std::uint64_t kFnvOffset = 14695981039346656037ULL;
constexpr std::uint64_t kFnvPrime = 1099511628211ULL;

struct Golden {
  std::string fixture;
  std::string model_sha256;
  std::string frontend_reference;
  std::string inference_reference;
  std::size_t sample_count = 0;
  std::size_t feature_size = 0;
  std::vector<std::uint64_t> feature_hashes;
  std::vector<std::uint8_t> scores;
  std::vector<std::uint8_t> model_probe_scores;
};

bool ReadLabel(std::istream& input, const char* expected) {
  std::string actual;
  if (!(input >> actual) || actual != expected) {
    std::cerr << "golden parse error: expected " << expected << ", got "
              << (actual.empty() ? "<eof>" : actual) << '\n';
    return false;
  }
  return true;
}

bool LoadGolden(const std::string& path, Golden* golden) {
  std::ifstream input(path);
  if (!input) {
    std::cerr << "cannot open golden vector " << path << '\n';
    return false;
  }
  std::size_t feature_frame_count = 0;
  std::size_t score_count = 0;
  std::size_t model_probe_frame_count = 0;
  std::size_t model_probe_score_count = 0;
  std::string magic;
  if (!(input >> magic) || magic != "TATER_MWW_GOLDEN_V1" ||
      !ReadLabel(input, "fixture") || !(input >> golden->fixture) ||
      !ReadLabel(input, "model_sha256") ||
      !(input >> golden->model_sha256) ||
      !ReadLabel(input, "frontend_reference") ||
      !(input >> golden->frontend_reference) ||
      !ReadLabel(input, "inference_reference") ||
      !(input >> golden->inference_reference) ||
      !ReadLabel(input, "sample_count") || !(input >> golden->sample_count) ||
      !ReadLabel(input, "feature_size") || !(input >> golden->feature_size) ||
      !ReadLabel(input, "feature_frame_count") ||
      !(input >> feature_frame_count) || !ReadLabel(input, "score_count") ||
      !(input >> score_count) ||
      !ReadLabel(input, "model_probe_frame_count") ||
      !(input >> model_probe_frame_count) ||
      !ReadLabel(input, "model_probe_score_count") ||
      !(input >> model_probe_score_count) ||
      model_probe_frame_count != kModelProbeFrames ||
      !ReadLabel(input, "feature_hashes")) {
    return false;
  }

  golden->feature_hashes.reserve(feature_frame_count);
  for (std::size_t i = 0; i < feature_frame_count; ++i) {
    std::string encoded;
    if (!(input >> encoded)) {
      std::cerr << "golden parse error: missing feature hash " << i << '\n';
      return false;
    }
    try {
      golden->feature_hashes.push_back(std::stoull(encoded, nullptr, 16));
    } catch (const std::exception&) {
      std::cerr << "golden parse error: invalid feature hash " << encoded
                << '\n';
      return false;
    }
  }
  if (!ReadLabel(input, "scores_u8")) {
    return false;
  }
  golden->scores.reserve(score_count);
  for (std::size_t i = 0; i < score_count; ++i) {
    unsigned int value = 0;
    if (!(input >> value) || value > std::numeric_limits<std::uint8_t>::max()) {
      std::cerr << "golden parse error: invalid score " << i << '\n';
      return false;
    }
    golden->scores.push_back(static_cast<std::uint8_t>(value));
  }
  if (!ReadLabel(input, "model_probe_scores_u8")) {
    return false;
  }
  golden->model_probe_scores.reserve(model_probe_score_count);
  for (std::size_t i = 0; i < model_probe_score_count; ++i) {
    unsigned int value = 0;
    if (!(input >> value) || value > std::numeric_limits<std::uint8_t>::max()) {
      std::cerr << "golden parse error: invalid model probe score " << i
                << '\n';
      return false;
    }
    golden->model_probe_scores.push_back(static_cast<std::uint8_t>(value));
  }
  return true;
}

std::vector<std::int16_t> GenerateIntegerSweep() {
  std::vector<std::int16_t> pcm(kFixtureSamples);
  std::uint32_t state = 0x13579BDFU;
  for (std::size_t i = 0; i < pcm.size(); ++i) {
    state = state * 1664525U + 1013904223U;
    const std::int64_t noise = static_cast<std::int64_t>((state >> 20) & 0xFFFU) - 2048;
    const std::size_t segment = i / 4000;
    std::int64_t value = 0;
    switch (segment) {
      case 0:
        value = noise * 2;
        break;
      case 1:
        value = (static_cast<std::int64_t>((i * 37) % 512) - 256) * 48 + noise * 2;
        break;
      case 2:
        value = ((i / 23) % 2 == 0 ? 10000 : -10000) + noise * 3;
        break;
      case 3:
        value = (static_cast<std::int64_t>((i * i + 17 * i) % 1024) - 512) * 24 +
                (i % 997 < 8 ? (i % 2 == 0 ? 14000 : -14000) : 0) + noise;
        break;
      case 4:
        value = (static_cast<std::int64_t>((i * 31) % 1024) - 512) * 20 +
                (static_cast<std::int64_t>((i * 7) % 256) - 128) * 30 + noise * 2;
        break;
      case 5:
        value = ((i / 320) % 2 == 0)
                    ? (static_cast<std::int64_t>((i * 53) % 1024) - 512) * 28 + noise * 2
                    : noise;
        break;
      case 6:
        value = noise * 12 + ((i / 41) % 2 == 0 ? 5000 : -5000);
        break;
      default:
        value = (static_cast<std::int64_t>((i * 11) % 2048) - 1024) * 14 +
                (static_cast<std::int64_t>((i * 97) % 256) - 128) * 12 + noise * 3;
        break;
    }
    value = std::clamp<std::int64_t>(value, -32768, 32767);
    pcm[i] = static_cast<std::int16_t>(value);
  }
  return pcm;
}

std::uint64_t HashFeature(const std::uint16_t* values,
                          std::size_t value_count) {
  std::uint64_t hash = kFnvOffset;
  for (std::size_t i = 0; i < value_count; ++i) {
    hash ^= values[i] & 0xFFU;
    hash *= kFnvPrime;
    hash ^= values[i] >> 8;
    hash *= kFnvPrime;
  }
  return hash;
}

std::vector<std::array<std::uint16_t, kFeatureSize>> GenerateModelProbe() {
  std::vector<std::array<std::uint16_t, kFeatureSize>> frames(kModelProbeFrames);
  std::uint32_t state = 0x2468ACE1U;
  for (std::size_t frame = 0; frame < frames.size(); ++frame) {
    for (std::size_t bin = 0; bin < frames[frame].size(); ++bin) {
      state = state * 1664525U + 1013904223U;
      const std::uint32_t jitter = ((state >> 16) & 0xFFFFU) % 80U;
      frames[frame][bin] = static_cast<std::uint16_t>(
          (frame * 17 + bin * 29 + jitter) % 667);
    }
  }
  return frames;
}

void ObserveFeature(const std::uint16_t* values, std::size_t value_count,
                    void* context) {
  auto* hashes = static_cast<std::vector<std::uint64_t>*>(context);
  hashes->push_back(HashFeature(values, value_count));
}

std::vector<std::uint8_t> QuantizeScores(const std::vector<float>& scores) {
  std::vector<std::uint8_t> result;
  result.reserve(scores.size());
  for (float score : scores) {
    result.push_back(static_cast<std::uint8_t>(std::clamp<long>(
        std::lround(score * 255.0f), 0, 255)));
  }
  return result;
}

bool Compare(const char* pass_name, const Golden& golden,
             const std::vector<std::uint64_t>& feature_hashes,
             const std::vector<float>& scores) {
  if (feature_hashes.size() != golden.feature_hashes.size()) {
    std::cerr << pass_name << ": feature frame count " << feature_hashes.size()
              << ", expected " << golden.feature_hashes.size() << '\n';
    return false;
  }
  for (std::size_t i = 0; i < feature_hashes.size(); ++i) {
    if (feature_hashes[i] != golden.feature_hashes[i]) {
      std::cerr << pass_name << ": feature frame " << i << " hash 0x"
                << std::hex << feature_hashes[i] << ", expected 0x"
                << golden.feature_hashes[i] << std::dec << '\n';
      return false;
    }
  }
  const auto quantized_scores = QuantizeScores(scores);
  if (quantized_scores.size() != golden.scores.size()) {
    std::cerr << pass_name << ": score count " << quantized_scores.size()
              << ", expected " << golden.scores.size() << '\n';
    return false;
  }
  for (std::size_t i = 0; i < quantized_scores.size(); ++i) {
    if (quantized_scores[i] != golden.scores[i]) {
      std::cerr << pass_name << ": score " << i << " is "
                << static_cast<unsigned int>(quantized_scores[i])
                << ", expected "
                << static_cast<unsigned int>(golden.scores[i]) << '\n';
      return false;
    }
  }
  return true;
}

bool ProcessOneShot(tater::microwakeword::Engine* engine,
                    const std::vector<std::int16_t>& pcm,
                    std::vector<float>* scores, std::string* error) {
  scores->assign(256, 0.0f);
  std::size_t score_count = 0;
  if (!engine->Process(pcm.data(), pcm.size(), scores->data(), scores->size(),
                       &score_count, error)) {
    return false;
  }
  scores->resize(score_count);
  return true;
}

bool ProcessChunked(tater::microwakeword::Engine* engine,
                    const std::vector<std::int16_t>& pcm,
                    std::vector<float>* scores, std::string* error) {
  constexpr std::array<std::size_t, 9> kChunks = {
      1, 159, 17, 463, 320, 777, 13, 2048, 91};
  scores->clear();
  std::size_t offset = 0;
  std::size_t chunk_index = 0;
  while (offset < pcm.size()) {
    const std::size_t count =
        std::min(kChunks[chunk_index++ % kChunks.size()], pcm.size() - offset);
    std::array<float, 32> chunk_scores{};
    std::size_t score_count = 0;
    if (!engine->Process(pcm.data() + offset, count, chunk_scores.data(),
                         chunk_scores.size(), &score_count, error)) {
      return false;
    }
    scores->insert(scores->end(), chunk_scores.begin(),
                   chunk_scores.begin() + score_count);
    offset += count;
  }
  return true;
}

bool ProcessModelProbe(tater::microwakeword::Engine* engine,
                       std::vector<float>* scores, std::string* error) {
  scores->clear();
  for (const auto& frame : GenerateModelProbe()) {
    float score = 0.0f;
    bool produced_score = false;
    if (!engine->ProcessFeatureForDiagnostics(
            frame.data(), frame.size(), &score, &produced_score, error)) {
      return false;
    }
    if (produced_score) {
      scores->push_back(score);
    }
  }
  return true;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc != 3) {
    std::cerr << "usage: tater_mww_golden MODEL.tflite VECTOR.golden\n";
    return 2;
  }
  Golden golden;
  if (!LoadGolden(argv[2], &golden)) {
    return 2;
  }
  if (golden.fixture != "integer_sweep_v1" ||
      golden.sample_count != kFixtureSamples ||
      golden.feature_size != kFeatureSize) {
    std::cerr << "golden vector does not describe integer_sweep_v1\n";
    return 2;
  }

  std::ifstream model_stream(argv[1], std::ios::binary);
  if (!model_stream) {
    std::cerr << "cannot open model " << argv[1] << '\n';
    return 2;
  }
  std::vector<std::uint8_t> model(
      (std::istreambuf_iterator<char>(model_stream)),
      std::istreambuf_iterator<char>());

  tater::microwakeword::EngineConfig config;
  config.tensor_arena_size = 192 * 1024;
  std::string error;
  auto engine = tater::microwakeword::Engine::Create(
      model.data(), model.size(), config, &error);
  if (engine == nullptr) {
    std::cerr << "create failed: " << error << '\n';
    return 1;
  }

  const auto pcm = GenerateIntegerSweep();
  std::vector<std::uint64_t> feature_hashes;
  engine->SetFeatureObserver(ObserveFeature, &feature_hashes);
  std::vector<float> scores;
  if (!ProcessOneShot(engine.get(), pcm, &scores, &error)) {
    std::cerr << "one-shot processing failed: " << error << '\n';
    return 1;
  }
  if (!Compare("one-shot", golden, feature_hashes, scores)) {
    return 1;
  }

  if (!engine->Reset(&error)) {
    std::cerr << "reset failed: " << error << '\n';
    return 1;
  }
  feature_hashes.clear();
  scores.clear();
  if (!ProcessChunked(engine.get(), pcm, &scores, &error)) {
    std::cerr << "chunked processing failed: " << error << '\n';
    return 1;
  }
  if (!Compare("chunked", golden, feature_hashes, scores)) {
    return 1;
  }

  if (!engine->Reset(&error)) {
    std::cerr << "model probe reset failed: " << error << '\n';
    return 1;
  }
  engine->SetFeatureObserver(nullptr, nullptr);
  scores.clear();
  if (!ProcessModelProbe(engine.get(), &scores, &error)) {
    std::cerr << "model probe failed: " << error << '\n';
    return 1;
  }
  const auto model_probe_scores = QuantizeScores(scores);
  if (model_probe_scores != golden.model_probe_scores) {
    std::size_t mismatch_count = 0;
    const std::size_t common_size =
        std::min(model_probe_scores.size(), golden.model_probe_scores.size());
    for (std::size_t i = 0; i < common_size; ++i) {
      if (model_probe_scores[i] != golden.model_probe_scores[i]) {
        if (mismatch_count < 10) {
          std::cerr << "model probe score " << i << ": got "
                    << static_cast<unsigned int>(model_probe_scores[i])
                    << ", expected "
                    << static_cast<unsigned int>(golden.model_probe_scores[i])
                    << '\n';
        }
        ++mismatch_count;
      }
    }
    mismatch_count += model_probe_scores.size() > common_size
                          ? model_probe_scores.size() - common_size
                          : golden.model_probe_scores.size() - common_size;
    std::cerr << "model probe mismatches: " << mismatch_count << " of "
              << std::max(model_probe_scores.size(),
                          golden.model_probe_scores.size())
              << '\n';
    return 1;
  }

  std::cout << "golden parity passed: " << feature_hashes.size()
            << " feature frames, " << golden.scores.size()
            << " PCM scores, " << scores.size() << " model probe scores; frontend="
            << golden.frontend_reference << ", inference="
            << golden.inference_reference << '\n';
  return 0;
}
