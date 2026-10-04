package crypt

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGetVkey(t *testing.T) {
	v1 := GetVkey()
	v2 := GetVkey()
	if len(v1) != 32 {
		t.Fatalf("vkey length = %d, want 32 (128 bit)", len(v1))
	}
	if _, err := hex.DecodeString(v1); err != nil {
		t.Fatalf("vkey is not hex: %v", err)
	}
	if v1 == v2 {
		t.Fatal("two vkeys identical, expect random")
	}
}

func TestGetRandomString(t *testing.T) {
	charset := "0123456789abcdefghijklmnopqrstuvwxyz"
	for _, l := range []int{8, 16, 32} {
		s := GetRandomString(l)
		if len(s) != l {
			t.Fatalf("len = %d, want %d", len(s), l)
		}
		for _, c := range s {
			if !strings.ContainsRune(charset, c) {
				t.Fatalf("char %q outside charset", c)
			}
		}
	}
}
