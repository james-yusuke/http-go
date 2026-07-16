//go:build arm64 && !purego

#include "textflag.h"

TEXT ·indexByteAsm(SB), NOSPLIT, $0-40
	MOVD b_base+0(FP), R0
	MOVD b_len+8(FP), R1
	MOVBU c+24(FP), R2
	MOVD $0, R3
	CMP $16, R1
	BLT loop
	VMOV R2, V0.B16
vectorloop:
	CMP $16, R1
	BLT loop
	VLD1 (R0), [V1.B16]
	VCMEQ V0.B16, V1.B16, V2.B16
	VADDP V2.B16, V2.B16, V3.B16
	VADDP V3.B16, V3.B16, V4.B16
	VADDP V4.B16, V4.B16, V5.B16
	VADDP V5.B16, V5.B16, V6.B16
	VMOV V6.D[0], R5
	CBNZ R5, loop
	ADD $16, R0
	ADD $16, R3
	SUB $16, R1
	B vectorloop
loop:
	CMP $0, R1
	BEQ notfound
	MOVBU (R0), R4
	CMP R2, R4
	BEQ found
	ADD $1, R0
	ADD $1, R3
	SUB $1, R1
	B loop
found:
	MOVD R3, ret+32(FP)
	RET
notfound:
	MOVD $-1, R3
	MOVD R3, ret+32(FP)
	RET
