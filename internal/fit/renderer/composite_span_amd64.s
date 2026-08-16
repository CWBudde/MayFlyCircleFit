// Exact float64 SSE2 span compositor for opaque NRGBA canvases.
//
// This is the baseline-AMD64 counterpart of the NEON kernel in
// composite_span_arm64.s and, like it, is byte-identical to
// compositeOpaqueSpanScalar rather than an approximation.
//
// The op order is load-bearing. compositeOpaqueSpanScalar compiles on amd64 to
// MULSD/ADDSD pairs - Go's amd64 backend does not contract a*b+c into an FMA -
// and this kernel reproduces that sequence with MULPD/ADDPD:
//
//	v = pix * inv255
//	v = v * bgBlend + fg
//	v = v * 255 + 0.5
//	byte = trunc(v)
//
// Do not fold any of those pairs into an FMA. It would change the rounding and
// silently break byte parity with the scalar path, which is exactly the failure
// mode that looks like a harmless precision artifact. TestCompositeSpanExact
// FusionContract pins this. ARM64 has the mirror-image dependency: its NEON
// kernel needs the fusion that Go's arm64 backend does perform.
//
// Alpha passes through arithmetically rather than by masking the store. Lane 3
// of each constant vector is (inv255=1, bgBlend=1, fg=0, scale=1, half=0.5), so
// the chain evaluates to a+0.5 and truncates back to a. That is exact for every
// byte value, because integers below 2^53 are exact in float64.
//
// constants points at five consecutive four-lane float64 vectors, in the order
// the chain uses them: inv255, bgBlend, fg, scale, half. This kernel reads that
// block as ten two-lane vectors, so the layout is shared with the AVX2 kernel
// that lands separately.

#include "textflag.h"

// func compositeSpanExactSSE2(pix *byte, pairs int, constants *float64)
//
// Two pixels per iteration. An XMM register holds two float64 lanes, so one
// pixel needs two registers and a pair needs four.
//
// SSE2 has no PMOVZXBD, so bytes widen in two PUNPCK steps against a zero
// register, and no VCVTDQ2PD-from-the-upper-half, so the high two dwords are
// brought down with PSHUFD before the second CVTPL2PD. Destructive two-operand
// encodings are why the constants occupy ten registers and the working set only
// five - that is the whole register file, and it is why this kernel is not
// unrolled further.
//
// All ten constant loads are MOVOU: Go aligns a [20]float64 to eight bytes,
// so a 16-byte-aligned load or an SSE2 memory operand would fault.
TEXT ·compositeSpanExactSSE2(SB), NOSPLIT, $0-24
	MOVQ pix+0(FP), SI
	MOVQ pairs+8(FP), CX
	MOVQ constants+16(FP), DX

	MOVOU 0(DX), X8     // inv255,  lanes 0-1
	MOVOU 16(DX), X9    // inv255,  lanes 2-3
	MOVOU 32(DX), X10   // bgBlend, lanes 0-1
	MOVOU 48(DX), X11   // bgBlend, lanes 2-3
	MOVOU 64(DX), X12   // fg,      lanes 0-1
	MOVOU 80(DX), X13   // fg,      lanes 2-3
	MOVOU 96(DX), X14   // scale,   lanes 0-1
	MOVOU 112(DX), X15  // scale,   lanes 2-3
	MOVOU 128(DX), X4   // half,    lanes 0-1
	MOVOU 144(DX), X5   // half,    lanes 2-3
	PXOR  X7, X7

	TESTQ CX, CX
	JZ    sse2_done

sse2_loop:
	MOVQ      (SI), X0   // eight bytes, two pixels
	PUNPCKLBW X7, X0     // eight words
	MOVOU     X0, X3
	PUNPCKLWL X7, X0     // pixel 0 as four dwords
	PUNPCKHWL X7, X3     // pixel 1 as four dwords

	CVTPL2PD X0, X1      // pixel 0, R G
	PSHUFD   $0x0E, X0, X0
	CVTPL2PD X0, X2      // pixel 0, B A
	CVTPL2PD X3, X6      // pixel 1, R G
	PSHUFD   $0x0E, X3, X3
	CVTPL2PD X3, X3      // pixel 1, B A

	MULPD X8, X1
	MULPD X9, X2
	MULPD X8, X6
	MULPD X9, X3

	MULPD X10, X1
	MULPD X11, X2
	MULPD X10, X6
	MULPD X11, X3

	ADDPD X12, X1
	ADDPD X13, X2
	ADDPD X12, X6
	ADDPD X13, X3

	MULPD X14, X1
	MULPD X15, X2
	MULPD X14, X6
	MULPD X15, X3

	ADDPD X4, X1
	ADDPD X5, X2
	ADDPD X4, X6
	ADDPD X5, X3

	CVTTPD2PL X1, X1     // two dwords in the low half, high half zeroed
	CVTTPD2PL X2, X2
	CVTTPD2PL X6, X6
	CVTTPD2PL X3, X3

	PUNPCKLQDQ X2, X1    // pixel 0 as four dwords
	PUNPCKLQDQ X3, X6    // pixel 1 as four dwords
	PACKSSLW   X6, X1    // both pixels as eight words
	PACKUSWB   X1, X1    // both pixels as eight bytes, low half
	MOVQ       X1, (SI)

	ADDQ $8, SI
	DECQ CX
	JNZ  sse2_loop

sse2_done:
	RET
