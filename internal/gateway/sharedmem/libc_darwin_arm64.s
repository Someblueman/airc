//go:build darwin && arm64

#include "textflag.h"

TEXT libc_shm_open_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_shm_open(SB)
GLOBL ·libcSHMOpen(SB), RODATA, $8
DATA ·libcSHMOpen(SB)/8, $libc_shm_open_trampoline<>(SB)
