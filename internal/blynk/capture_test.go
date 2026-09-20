package blynk

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// capturePath points at the recording of a real Plaato Keg session: 117 files,
// each one TCP segment as the device sent it.
const capturePath = "../../testdata/capture"

// captureFiles returns the capture in the order the device sent it (0.bin,
// 1.bin, ... 116.bin — numerically, not lexicographically).
func captureFiles(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(capturePath, "*.bin"))
	if err != nil {
		t.Fatalf("glob capture: %v", err)
	}
	if len(names) == 0 {
		t.Fatalf("no capture files under %s", capturePath)
	}
	sort.Slice(names, func(i, j int) bool {
		return captureIndex(names[i]) < captureIndex(names[j])
	})
	return names
}

func captureIndex(path string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".bin"))
	return n
}

// Every segment in the capture must frame cleanly, with nothing left buffered.
func TestFrameWholeCapture(t *testing.T) {
	var f Framer
	total := 0
	for _, name := range captureFiles(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		frames, err := f.Feed(data)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(name), err)
		}
		if len(frames) == 0 {
			t.Errorf("%s: no frames decoded from %d bytes", filepath.Base(name), len(data))
		}
		if f.Buffered() != 0 {
			t.Errorf("%s: %d bytes left buffered — a frame boundary was misread",
				filepath.Base(name), f.Buffered())
		}
		total += len(frames)
	}
	if total < 117 {
		t.Errorf("decoded %d frames from the capture, expected at least one per file", total)
	}
	t.Logf("decoded %d frames from %d captured segments", total, len(captureFiles(t)))
}

// Feeding the entire capture as one undifferentiated stream, one byte at a
// time, must produce exactly the same frames as feeding it segment by segment.
func TestFrameCaptureIsSegmentationIndependent(t *testing.T) {
	var perSegment []Frame
	var f1 Framer
	var all []byte
	for _, name := range captureFiles(t) {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		all = append(all, data...)
		frames, err := f1.Feed(data)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(name), err)
		}
		perSegment = append(perSegment, frames...)
	}

	var byteWise []Frame
	var f2 Framer
	for _, b := range all {
		frames, err := f2.Feed([]byte{b})
		if err != nil {
			t.Fatalf("byte-wise feed: %v", err)
		}
		byteWise = append(byteWise, frames...)
	}

	if len(perSegment) != len(byteWise) {
		t.Fatalf("segment-wise decoded %d frames, byte-wise decoded %d", len(perSegment), len(byteWise))
	}
	for i := range perSegment {
		a, b := perSegment[i], byteWise[i]
		if a.Cmd != b.Cmd || a.MsgID != b.MsgID || string(a.Body) != string(b.Body) {
			t.Fatalf("frame %d differs: %+v vs %+v", i, a, b)
		}
	}
}
