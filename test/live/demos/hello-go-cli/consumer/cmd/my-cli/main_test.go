package main

import "testing"

func TestGreeting(t *testing.T) {
	for name, want := range map[string]string{"": "Hello, world!", "Ada": "Hello, Ada!"} {
		if got := greeting(name); got != want {
			t.Errorf("greeting(%q) = %q, want %q", name, got, want)
		}
	}
}
