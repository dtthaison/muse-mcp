package main

import (
	"os"
	"testing"
)

// /dev/null is a character device but not a terminal: a command run by a script has it as stdin
// and must not be asked to approve anything.
func TestDevNullIsNotATerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null counted as a terminal")
	}
}

func TestPipeIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Fatal("a pipe counted as a terminal")
	}
}
