cmake_minimum_required(VERSION 3.22.1)
project(tater_microwakeword LANGUAGES C CXX)

set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

option(TATER_MWW_BUILD_SMOKE_TEST "Build the native model smoke test" ON)
set(TATER_MWW_TEST_MODEL "" CACHE FILEPATH "Optional model for ctest")
set(TATER_MWW_GOLDEN_FILE "" CACHE FILEPATH "Optional golden parity vector")

add_compile_options(-O3 -ffunction-sections -fdata-sections)
if(NOT APPLE)
  add_link_options(-Wl,--gc-sections)
endif()

include(FetchContent)

# Every archive is content-addressed. These versions match the Home Assistant
# Android microWakeWord runtime reviewed when this port was started. A pin bump
# must update the hash, the matching TFLM third-party pins, and the runtime
# version string in c_api.cc in one change.
FetchContent_Declare(
  kissfft
  URL https://github.com/mborgerding/kissfft/archive/7bce4153c6bc8aba2db0e889e576f9d00505cbe1.tar.gz
  URL_HASH SHA256=7ad1124648a46977b16ddde03bf243bcd52fc452516c57016584ab4b4f2baadc
  DOWNLOAD_EXTRACT_TIMESTAMP TRUE
  SOURCE_SUBDIR tater_mww_sources_only
)
set(KISSFFT_STATIC ON CACHE BOOL "" FORCE)
set(KISSFFT_TEST OFF CACHE BOOL "" FORCE)
set(KISSFFT_TOOLS OFF CACHE BOOL "" FORCE)

FetchContent_Declare(
  tflite_micro
  URL https://github.com/tensorflow/tflite-micro/archive/2747abd5c82a95fb1624106a946fc671c31f16e8.tar.gz
  URL_HASH SHA256=6ac0c8ef35f267cf3baf05dc094d4fd158864d4cc830d490b752994e58add8c8
  DOWNLOAD_EXTRACT_TIMESTAMP TRUE
  SOURCE_SUBDIR tater_mww_sources_only
)

FetchContent_Declare(
  flatbuffers
  URL https://github.com/google/flatbuffers/archive/0100f6a5779831fa7a651e4b67ef389a8752bd9b.tar.gz
  URL_HASH SHA256=85db3520acc4010b21984e2fb5ead3ec0d2c48df8009b614cb73562a82846554
  DOWNLOAD_EXTRACT_TIMESTAMP TRUE
  SOURCE_SUBDIR tater_mww_sources_only
)
set(FLATBUFFERS_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(FLATBUFFERS_INSTALL OFF CACHE BOOL "" FORCE)
set(FLATBUFFERS_BUILD_FLATC OFF CACHE BOOL "" FORCE)
set(FLATBUFFERS_BUILD_FLATHASH OFF CACHE BOOL "" FORCE)

FetchContent_Declare(
  gemmlowp
  URL https://github.com/google/gemmlowp/archive/fda83bdc38b118cc6b56753bd540caa49e570745.tar.gz
  URL_HASH SHA256=0f990732a0d541be514dfc5c1c45969626e9f349faa851baec01b9a9dcb4ae4b
  DOWNLOAD_EXTRACT_TIMESTAMP TRUE
  SOURCE_SUBDIR tater_mww_sources_only
)

FetchContent_Declare(
  ruy
  URL https://github.com/google/ruy/archive/54774a7a2cf85963777289193629d4bd42de4a59.tar.gz
  URL_HASH SHA256=91993e7eb2aa56e62e9f4abc1158b2b46333574aa81f1a7431b06df80a42b7fa
  DOWNLOAD_EXTRACT_TIMESTAMP TRUE
  SOURCE_SUBDIR tater_mww_sources_only
)

FetchContent_MakeAvailable(kissfft tflite_micro flatbuffers gemmlowp ruy)

# microfrontend includes kiss_fftr through the layout used by TensorFlow's
# makefiles. kissfft keeps those files at its root, so reproduce that layout in
# the fetched build tree without changing the dependency source.
file(MAKE_DIRECTORY "${kissfft_SOURCE_DIR}/tools")
file(COPY_FILE "${kissfft_SOURCE_DIR}/kiss_fftr.c"
     "${kissfft_SOURCE_DIR}/tools/kiss_fftr.c" ONLY_IF_DIFFERENT)
file(COPY_FILE "${kissfft_SOURCE_DIR}/kiss_fftr.h"
     "${kissfft_SOURCE_DIR}/tools/kiss_fftr.h" ONLY_IF_DIFFERENT)

set(TFLM_ROOT "${tflite_micro_SOURCE_DIR}")
set(TFLM "${TFLM_ROOT}/tensorflow/lite/micro")
set(TFL "${TFLM_ROOT}/tensorflow/lite")
set(FRONTEND "${TFL}/experimental/microfrontend/lib")

add_library(tater_mww_frontend STATIC
  ${FRONTEND}/fft.cc
  ${FRONTEND}/fft_util.cc
  ${FRONTEND}/kiss_fft_int16.cc
  ${FRONTEND}/filterbank.c
  ${FRONTEND}/filterbank_util.c
  ${FRONTEND}/frontend.c
  ${FRONTEND}/frontend_util.c
  ${FRONTEND}/log_lut.c
  ${FRONTEND}/log_scale.c
  ${FRONTEND}/log_scale_util.c
  ${FRONTEND}/noise_reduction.c
  ${FRONTEND}/noise_reduction_util.c
  ${FRONTEND}/pcan_gain_control.c
  ${FRONTEND}/pcan_gain_control_util.c
  ${FRONTEND}/window.c
  ${FRONTEND}/window_util.c
)
target_include_directories(tater_mww_frontend SYSTEM PUBLIC
  ${TFLM_ROOT}
  ${kissfft_SOURCE_DIR}
)
target_compile_definitions(tater_mww_frontend PUBLIC FIXED_POINT=16)
target_compile_options(tater_mww_frontend PRIVATE
  -Wno-unused-parameter -Wno-sign-compare -ffast-math
)

add_library(tater_mww_tflm STATIC
  ${TFLM}/micro_interpreter.cc
  ${TFLM}/micro_allocator.cc
  ${TFLM}/micro_context.cc
  ${TFLM}/micro_op_resolver.cc
  ${TFLM}/micro_utils.cc
  ${TFLM}/micro_log.cc
  ${TFLM}/flatbuffer_utils.cc
  ${TFLM}/memory_helpers.cc
  ${TFLM}/micro_allocation_info.cc
  ${TFLM}/micro_interpreter_context.cc
  ${TFLM}/micro_interpreter_graph.cc
  ${TFLM}/micro_resource_variable.cc
  ${TFLM}/micro_profiler.cc
  ${TFLM}/micro_time.cc
  ${TFLM}/system_setup.cc
  ${TFLM}/fake_micro_context.cc
  ${TFLM}/debug_log.cc

  ${TFLM}/arena_allocator/single_arena_buffer_allocator.cc
  ${TFLM}/arena_allocator/non_persistent_arena_buffer_allocator.cc
  ${TFLM}/arena_allocator/persistent_arena_buffer_allocator.cc
  ${TFLM}/memory_planner/greedy_memory_planner.cc
  ${TFLM}/memory_planner/linear_memory_planner.cc
  ${TFLM}/memory_planner/non_persistent_buffer_planner_shim.cc
  ${TFLM}/tflite_bridge/micro_error_reporter.cc
  ${TFLM}/tflite_bridge/flatbuffer_conversions_bridge.cc

  ${TFLM_ROOT}/tensorflow/compiler/mlir/lite/core/api/error_reporter.cc
  ${TFLM_ROOT}/tensorflow/compiler/mlir/lite/schema/schema_utils.cc
  ${TFL}/core/c/common.cc
  ${TFL}/core/api/flatbuffer_conversions.cc
  ${TFL}/core/api/tensor_utils.cc
  ${TFL}/kernels/kernel_util.cc
  ${TFL}/kernels/internal/common.cc
  ${TFL}/kernels/internal/quantization_util.cc
  ${TFL}/kernels/internal/portable_tensor_utils.cc
  ${TFL}/kernels/internal/tensor_ctypes.cc
  ${TFL}/kernels/internal/runtime_shape.cc
  ${TFL}/kernels/internal/reference/portable_tensor_utils.cc

  ${TFLM}/kernels/conv.cc
  ${TFLM}/kernels/conv_common.cc
  ${TFLM}/kernels/depthwise_conv.cc
  ${TFLM}/kernels/depthwise_conv_common.cc
  ${TFLM}/kernels/fully_connected.cc
  ${TFLM}/kernels/fully_connected_common.cc
  ${TFLM}/kernels/pooling.cc
  ${TFLM}/kernels/pooling_common.cc
  ${TFLM}/kernels/reshape.cc
  ${TFLM}/kernels/reshape_common.cc
  ${TFLM}/kernels/softmax.cc
  ${TFLM}/kernels/softmax_common.cc
  ${TFLM}/kernels/logistic.cc
  ${TFLM}/kernels/logistic_common.cc
  ${TFLM}/kernels/add.cc
  ${TFLM}/kernels/add_common.cc
  ${TFLM}/kernels/mul.cc
  ${TFLM}/kernels/mul_common.cc
  ${TFLM}/kernels/quantize.cc
  ${TFLM}/kernels/quantize_common.cc
  ${TFLM}/kernels/dequantize.cc
  ${TFLM}/kernels/dequantize_common.cc
  ${TFLM}/kernels/activations.cc
  ${TFLM}/kernels/activations_common.cc
  ${TFLM}/kernels/pad.cc
  ${TFLM}/kernels/pad_common.cc
  ${TFLM}/kernels/concatenation.cc
  ${TFLM}/kernels/strided_slice.cc
  ${TFLM}/kernels/strided_slice_common.cc
  ${TFLM}/kernels/pack.cc
  ${TFLM}/kernels/unpack.cc
  ${TFLM}/kernels/kernel_util.cc
  ${TFLM}/kernels/micro_tensor_utils.cc
  ${TFLM}/kernels/call_once.cc
  ${TFLM}/kernels/var_handle.cc
  ${TFLM}/kernels/read_variable.cc
  ${TFLM}/kernels/assign_variable.cc
  ${TFLM}/kernels/split_v.cc
  ${TFLM}/kernels/reduce.cc
  ${TFLM}/kernels/reduce_common.cc
)
target_include_directories(tater_mww_tflm SYSTEM PUBLIC
  ${TFLM_ROOT}
  ${flatbuffers_SOURCE_DIR}/include
  ${gemmlowp_SOURCE_DIR}
  ${ruy_SOURCE_DIR}
)
target_compile_definitions(tater_mww_tflm PUBLIC
  TF_LITE_STATIC_MEMORY
  TF_LITE_DISABLE_X86_NEON
)
target_compile_options(tater_mww_tflm PRIVATE
  -Wno-unused-parameter
  -Wno-sign-compare
  -Wno-missing-field-initializers
  -Wno-unused-variable
  -Wno-unused-function
  -ffast-math
)
if(CMAKE_CXX_COMPILER_ID STREQUAL "AppleClang")
  # Upstream quantization helpers intentionally inspect infinities even though
  # this target uses fast-math. Keep that third-party diagnostic out of builds.
  target_compile_options(tater_mww_tflm PRIVATE -Wno-nan-infinity-disabled)
endif()

add_library(tater_mww_engine STATIC engine.cc)
target_include_directories(tater_mww_engine PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_compile_options(tater_mww_engine PRIVATE -Wall -Wextra -Wpedantic)
target_link_libraries(tater_mww_engine PUBLIC
  tater_mww_frontend
  tater_mww_tflm
  m
)
set_target_properties(tater_mww_engine PROPERTIES
  CXX_VISIBILITY_PRESET hidden
  VISIBILITY_INLINES_HIDDEN YES
)

add_library(tater_microwakeword SHARED c_api.cc)
target_include_directories(tater_microwakeword PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_compile_options(tater_microwakeword PRIVATE -Wall -Wextra -Wpedantic)
target_link_libraries(tater_microwakeword PRIVATE tater_mww_engine)
set_target_properties(tater_microwakeword PROPERTIES
  CXX_VISIBILITY_PRESET hidden
  VISIBILITY_INLINES_HIDDEN YES
  OUTPUT_NAME tater_microwakeword
)

if(ANDROID)
  # NDK r21's static STL keeps deployment to FireOS 5 to one self-contained
  # runtime .so rather than adding a matching libc++_shared.so.
  target_link_libraries(tater_microwakeword PRIVATE log atomic)
  target_link_options(tater_microwakeword PRIVATE
    "-Wl,--version-script=${CMAKE_CURRENT_SOURCE_DIR}/exports.map"
    -Wl,--strip-all
  )
endif()

if(TATER_MWW_BUILD_SMOKE_TEST AND NOT ANDROID)
  enable_testing()
  add_executable(tater_mww_smoke smoke_test.cc)
  target_compile_options(tater_mww_smoke PRIVATE -Wall -Wextra -Wpedantic)
  target_link_libraries(tater_mww_smoke PRIVATE tater_microwakeword)
  if(TATER_MWW_TEST_MODEL)
    add_test(NAME tater_mww_smoke
             COMMAND tater_mww_smoke ${TATER_MWW_TEST_MODEL})
    if(TATER_MWW_GOLDEN_FILE)
      add_executable(tater_mww_golden golden_test.cc)
      target_compile_options(tater_mww_golden PRIVATE -Wall -Wextra -Wpedantic)
      target_link_libraries(tater_mww_golden PRIVATE tater_mww_engine)
      add_test(NAME tater_mww_golden
               COMMAND tater_mww_golden
                       ${TATER_MWW_TEST_MODEL}
                       ${TATER_MWW_GOLDEN_FILE})
    endif()
  endif()
endif()
