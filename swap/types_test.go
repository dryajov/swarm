package swap

import (
	"crypto/rand"
	"math/big"
	"testing"
)

type Uint256TestCase struct {
	name         string
	baseInteger  *big.Int
	expectsError bool
}

func TestSetUint256(t *testing.T) {
	testCases := []Uint256TestCase{
		{
			name:         "base 0",
			baseInteger:  big.NewInt(0),
			expectsError: false,
		},
		// negative numbers
		{
			name:         "base -1",
			baseInteger:  big.NewInt(-1),
			expectsError: true,
		},
		{
			name:         "base -1 * 2^8",
			baseInteger:  new(big.Int).Mul(new(big.Int).Exp(big.NewInt(2), big.NewInt(8), nil), big.NewInt(-1)),
			expectsError: true,
		},
		{
			name:         "base -1 * 2^64",
			baseInteger:  new(big.Int).Mul(new(big.Int).Exp(big.NewInt(2), big.NewInt(64), nil), big.NewInt(-1)),
			expectsError: true,
		},
		// positive numbers
		{
			name:         "base 1",
			baseInteger:  big.NewInt(1),
			expectsError: false,
		},
		{
			name:         "base 2^8",
			baseInteger:  new(big.Int).Exp(big.NewInt(2), big.NewInt(8), nil),
			expectsError: false,
		},
		{
			name:         "base 2^128",
			baseInteger:  new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil),
			expectsError: false,
		},
		{
			name:         "base 2^256 - 1",
			baseInteger:  new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1)),
			expectsError: false,
		},
		{
			name:         "base 2^256",
			baseInteger:  new(big.Int).Add(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1)),
			expectsError: true,
		},
	}

	testSetUint256(t, testCases)
}

func testSetUint256(t *testing.T, testCases []Uint256TestCase) {
	t.Helper()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewUint256().Set(tc.baseInteger)
			if tc.expectsError && err == nil {
				t.Fatalf("expected error when creating new Uint256, but got none")
			}
			if !tc.expectsError && err != nil {
				t.Fatalf("got unexpected error when creating new Uint256: %v", err)
			}
		})
	}
}

func TestCopyUint256(t *testing.T) {
	t.Helper()

	r, err := rand.Int(rand.Reader, new(big.Int).Sub(maxUint256, minUint256)) // base for random
	if err != nil {
		t.Fatal(err)
	}

	randomUint256 := new(big.Int).Add(r, minUint256) // random is within [minUint256, maxUint256]

	u, err := NewUint256().Set(randomUint256)
	if err != nil {
		t.Fatal(err)
	}

	c := NewUint256().Copy(u)

	if c.Cmp(u) != 0 {
		t.Fatalf("copy of uint256 %v has an unequal value of %v", u, c)
	}

	if c == u {
		t.Fatalf("copy of uint256 %v shares memory with its base", u)
	}
}
