package main

import (
	"strings"
	"testing"
)

func TestGeneratedArgon2idPasswordVerifies(t *testing.T) {
	hash, err := hashPasswordWithError("correct horse battery staple 2026")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "argon2id$") {
		t.Fatalf("unexpected password hash format: %q", hash)
	}
	if !verifyPassword("correct horse battery staple 2026", hash) {
		t.Fatal("newly generated Argon2id password did not verify")
	}
	if verifyPassword("wrong password", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestArgon2idVerifierRejectsMalformedAndExcessiveParameters(t *testing.T) {
	cases := []string{
		"argon2id$v=19$t=3,m=65536,p=2$only-four-fields",
		"argon2id$v=18$t=3,m=65536,p=2$c2FsdHNhbHQ$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$t=3,m=2097152,p=2$c2FsdHNhbHQ$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$t=0,m=65536,p=2$c2FsdHNhbHQ$MDEyMzQ1Njc4OWFiY2RlZg",
		"argon2id$v=19$t=3,m=65536,p=0$c2FsdHNhbHQ$MDEyMzQ1Njc4OWFiY2RlZg",
	}
	for _, encoded := range cases {
		if verifyArgon2id("password", encoded) {
			t.Fatalf("malformed/excessive Argon2id hash accepted: %q", encoded)
		}
	}
}
