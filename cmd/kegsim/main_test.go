package main

import (
	"bytes"
	"strings"
	"testing"
)

const recordedToken = "00000000000000000000000000000001"

func TestSetTokenWithRecordedTokenLeavesCaptureUnchanged(t *testing.T) {
	original, err := loadCapture("../../testdata/capture")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, _ := loadCapture("../../testdata/capture")
	if err := setToken(rewritten, recordedToken); err != nil {
		t.Fatal(err)
	}

	for i := range original {
		if !bytes.Equal(original[i].data, rewritten[i].data) {
			t.Errorf("%s changed:\n got % X\nwant % X", original[i].name, rewritten[i].data, original[i].data)
		}
	}
}

func TestSetTokenReplacesOnlyTheToken(t *testing.T) {
	const token = "00000000000000000000000000000007"
	original, err := loadCapture("../../testdata/capture")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, _ := loadCapture("../../testdata/capture")
	if err := setToken(rewritten, token); err != nil {
		t.Fatal(err)
	}

	changed := 0
	for i := range original {
		want := bytes.ReplaceAll(original[i].data, []byte(recordedToken), []byte(token))
		if !bytes.Equal(want, rewritten[i].data) {
			t.Errorf("%s:\n got % X\nwant % X", original[i].name, rewritten[i].data, want)
		}
		if !bytes.Equal(original[i].data, rewritten[i].data) {
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("no segment carried the recorded token")
	}
}

func TestKegIDRejectsMalformedTokens(t *testing.T) {
	for _, bad := range []string{"", "7", strings.Repeat("0", 31), strings.Repeat("-", 32), " " + strings.Repeat("0", 31)} {
		if kegID.MatchString(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}
