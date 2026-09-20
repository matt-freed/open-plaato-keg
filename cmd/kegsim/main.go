// Command kegsim replays a recorded Plaato Keg session against a running
// server, for development and for checking a change against real device
// traffic.
//
// It mimics the firmware's behaviour of waiting for the server's
// acknowledgement before sending the next segment, so a missing or malformed
// acknowledgement shows up as a timeout rather than being silently tolerated.
//
//	go run ./cmd/kegsim -addr localhost:1234 -capture testdata/capture
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "localhost:1234", "address of the keg listener")
	capture := flag.String("capture", "testdata/capture", "directory of recorded segments")
	pause := flag.Duration("pause", 50*time.Millisecond, "delay between segments")
	timeout := flag.Duration("timeout", 5*time.Second, "how long to wait for each acknowledgement")
	loop := flag.Bool("loop", false, "replay continuously")
	flag.Parse()

	segments, err := loadCapture(*capture)
	if err != nil {
		log.Fatalf("load capture: %v", err)
	}
	log.Printf("loaded %d segments from %s", len(segments), *capture)

	for {
		if err := replay(*addr, segments, *pause, *timeout); err != nil {
			log.Fatalf("replay: %v", err)
		}
		if !*loop {
			return
		}
	}
}

type segment struct {
	name string
	data []byte
}

// loadCapture reads the recorded segments in the order the device sent them,
// which is numeric rather than lexicographic.
func loadCapture(dir string) ([]segment, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.bin"))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no .bin files in %s", dir)
	}
	sort.Slice(names, func(i, j int) bool {
		return captureIndex(names[i]) < captureIndex(names[j])
	})

	segments := make([]segment, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment{name: filepath.Base(name), data: data})
	}
	return segments, nil
}

func captureIndex(path string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".bin"))
	return n
}

func replay(addr string, segments []segment, pause, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	log.Printf("connected to %s", addr)

	ack := make([]byte, 5)
	for _, seg := range segments {
		if _, err := conn.Write(seg.data); err != nil {
			return fmt.Errorf("%s: write: %w", seg.name, err)
		}

		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, ack); err != nil {
			return fmt.Errorf("%s: no acknowledgement: %w", seg.name, err)
		}
		if ack[0] != 0x00 || ack[3] != 0x00 || ack[4] != 0xC8 {
			return fmt.Errorf("%s: acknowledgement was % X, want a 200 response", seg.name, ack)
		}

		msgID := int(seg.data[1])<<8 | int(seg.data[2])
		ackID := int(ack[1])<<8 | int(ack[2])
		if ackID != msgID {
			return fmt.Errorf("%s: acknowledged message %d, want %d — the firmware would not accept this",
				seg.name, ackID, msgID)
		}

		time.Sleep(pause)
	}

	log.Printf("replayed %d segments, every one acknowledged correctly", len(segments))
	return nil
}
