package main

import "testing"

func TestDefaultLayout(t *testing.T) {
	gav := Gav{GroupID: "my.group", ArtifactID: "a1", Version: "1.0.0"}
	want := "my/group/a1/1.0.0"
	got := gav.DefaultLayout()
	if want != got {
		t.Fatalf("Want %v but got %v", want, got)
	}
}
