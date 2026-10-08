#pragma once

// ONNX Runtime includes execinfo.h on every non-Android POSIX build even
// though release builds compile out stack capture. musl does not provide the
// glibc header, so this declaration-only compatibility header keeps the
// release build portable without adding an unused runtime dependency.

#ifdef __cplusplus
extern "C" {
#endif

int backtrace(void **buffer, int size);
char **backtrace_symbols(void *const *buffer, int size);

#ifdef __cplusplus
}
#endif
