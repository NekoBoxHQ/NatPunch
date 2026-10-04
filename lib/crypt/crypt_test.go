package crypt

import (
	"strings"
	"testing"
)

func TestGetVkey(t *testing.T) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	v1 := GetVkey()
	v2 := GetVkey()
	if len(v1) != 22 {
		t.Fatalf("vkey length = %d, want 22 (128 bit base62)", len(v1))
	}
	for _, c := range v1 {
		if !strings.ContainsRune(alphabet, c) {
			t.Fatalf("vkey char %q outside base62 charset", c)
		}
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
