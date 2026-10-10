//go:build darwin && arm64

#include "textflag.h"

TEXT libc_msgget_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_msgget(SB)
GLOBL ·libcMsgget(SB), RODATA, $8
DATA ·libcMsgget(SB)/8, $libc_msgget_trampoline<>(SB)

TEXT libc_msgctl_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_msgctl(SB)
GLOBL ·libcMsgctl(SB), RODATA, $8
DATA ·libcMsgctl(SB)/8, $libc_msgctl_trampoline<>(SB)

TEXT libc_msgsnd_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_msgsnd(SB)
GLOBL ·libcMsgsnd(SB), RODATA, $8
DATA ·libcMsgsnd(SB)/8, $libc_msgsnd_trampoline<>(SB)

TEXT libc_msgrcv_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_msgrcv(SB)
GLOBL ·libcMsgrcv(SB), RODATA, $8
DATA ·libcMsgrcv(SB)/8, $libc_msgrcv_trampoline<>(SB)
