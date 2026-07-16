//go:build amd64 && !purego

#include "textflag.h"

TEXT ·indexByteAsm(SB), NOSPLIT, $0-40
	MOVQ b_base+0(FP), AX
	MOVQ b_len+8(FP), CX
	MOVBLZX c+24(FP), R8
	XORQ BX, BX
loop:
	TESTQ CX, CX
	JE notfound
	MOVBLZX (AX), DX
	CMPB DL, R8B
	JE found
	INCQ AX
	INCQ BX
	DECQ CX
	JMP loop
found:
	MOVQ BX, ret+32(FP)
	RET
notfound:
	MOVQ $-1, ret+32(FP)
	RET

// func indexByteAVX2(b []byte, c byte) int
TEXT ·indexByteAVX2(SB), NOSPLIT, $0-40
	MOVQ b_base+0(FP), SI
	MOVQ b_len+8(FP), CX
	MOVBLZX c+24(FP), AX
	MOVD AX, X0
	VPBROADCASTB X0, Y1
	XORQ DI, DI
avxloop:
	CMPQ CX, $32
	JLT avxtail
	VMOVDQU (SI)(DI*1), Y2
	VPCMPEQB Y1, Y2, Y3
	VPMOVMSKB Y3, DX
	TESTL DX, DX
	JNZ avxfound
	ADDQ $32, DI
	SUBQ $32, CX
	JMP avxloop
avxfound:
	BSFL DX, DX
	ADDQ DI, DX
	MOVQ DX, ret+32(FP)
	VZEROUPPER
	RET
avxtail:
	VZEROUPPER
	TESTQ CX, CX
	JE avxmissing
	MOVBLZX (SI)(DI*1), DX
	CMPB DL, AL
	JE avxtailfound
	INCQ DI
	DECQ CX
	JMP avxtail
avxtailfound:
	MOVQ DI, ret+32(FP)
	RET
avxmissing:
	MOVQ $-1, ret+32(FP)
	RET
