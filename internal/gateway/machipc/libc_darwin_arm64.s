//go:build darwin && arm64

#include "textflag.h"

TEXT libc_mach_msg_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_msg(SB)
GLOBL ·libcMachMsg(SB), RODATA, $8
DATA ·libcMachMsg(SB)/8, $libc_mach_msg_trampoline<>(SB)
TEXT libc_mach_port_allocate_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_port_allocate(SB)
GLOBL ·libcMachPortAllocate(SB), RODATA, $8
DATA ·libcMachPortAllocate(SB)/8, $libc_mach_port_allocate_trampoline<>(SB)
TEXT libc_mach_port_insert_right_trampoline<>(SB),NOSPLIT,$0-0
	JMP libc_mach_port_insert_right(SB)
GLOBL ·libcMachPortInsertRight(SB), RODATA, $8
DATA ·libcMachPortInsertRight(SB)/8, $libc_mach_port_insert_right_trampoline<>(SB)
TEXT libc_mach_port_set_attributes_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_port_set_attributes(SB)
GLOBL ·libcMachPortSetAttributes(SB), RODATA, $8
DATA ·libcMachPortSetAttributes(SB)/8, $libc_mach_port_set_attributes_trampoline<>(SB)
TEXT libc_mach_port_deallocate_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_port_deallocate(SB)
GLOBL ·libcMachPortDeallocate(SB), RODATA, $8
DATA ·libcMachPortDeallocate(SB)/8, $libc_mach_port_deallocate_trampoline<>(SB)
TEXT libc_mach_port_mod_refs_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_port_mod_refs(SB)
GLOBL ·libcMachPortModRefs(SB), RODATA, $8
DATA ·libcMachPortModRefs(SB)/8, $libc_mach_port_mod_refs_trampoline<>(SB)
TEXT libc_mach_port_get_refs_trampoline<>(SB),NOSPLIT,$0-0
	JMP libc_mach_port_get_refs(SB)
GLOBL ·libcMachPortGetRefs(SB), RODATA, $8
DATA ·libcMachPortGetRefs(SB)/8, $libc_mach_port_get_refs_trampoline<>(SB)
TEXT libc_mach_msg_destroy_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_mach_msg_destroy(SB)
GLOBL ·libcMachMsgDestroy(SB), RODATA, $8
DATA ·libcMachMsgDestroy(SB)/8, $libc_mach_msg_destroy_trampoline<>(SB)
TEXT libc_bootstrap_register_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_bootstrap_register(SB)
GLOBL ·libcBootstrapRegister(SB), RODATA, $8
DATA ·libcBootstrapRegister(SB)/8, $libc_bootstrap_register_trampoline<>(SB)
TEXT libc_bootstrap_lookup_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_bootstrap_lookup(SB)
GLOBL ·libcBootstrapLookup(SB), RODATA, $8
DATA ·libcBootstrapLookup(SB)/8, $libc_bootstrap_lookup_trampoline<>(SB)
TEXT libc_audit_token_euid_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_audit_token_euid(SB)
GLOBL ·libcAuditTokenEUID(SB), RODATA, $8
DATA ·libcAuditTokenEUID(SB)/8, $libc_audit_token_euid_trampoline<>(SB)
TEXT libc_audit_token_pid_trampoline<>(SB),NOSPLIT,$0-0
 JMP libc_audit_token_pid(SB)
GLOBL ·libcAuditTokenPID(SB), RODATA, $8
DATA ·libcAuditTokenPID(SB)/8, $libc_audit_token_pid_trampoline<>(SB)

GLOBL ·machTaskSelfPtr(SB), RODATA, $8
DATA ·machTaskSelfPtr(SB)/8, $libc_mach_task_self(SB)
TEXT ·machTaskSelf(SB),NOSPLIT,$0-8
	MOVD ·machTaskSelfPtr(SB), R0
	MOVWU (R0), R0
 MOVD R0, ret+0(FP)
 RET
GLOBL ·bootstrapPortPtr(SB), RODATA, $8
DATA ·bootstrapPortPtr(SB)/8, $libc_bootstrap_port(SB)
TEXT ·bootstrapPort(SB),NOSPLIT,$0-8
	MOVD ·bootstrapPortPtr(SB), R0
	MOVWU (R0), R0
 MOVD R0, ret+0(FP)
 RET
