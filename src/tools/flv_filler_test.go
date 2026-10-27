package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type fillerFixtureOptions struct {
	filler    bool
	nalLength int
	mutation  string
}

func appendFixtureTag(data []byte, kind byte, timestamp uint32, payload []byte) []byte {
	n := len(payload)
	header := []byte{kind, byte(n >> 16), byte(n >> 8), byte(n), byte(timestamp >> 16), byte(timestamp >> 8), byte(timestamp), byte(timestamp >> 24), 0, 0, 0}
	data = append(data, header...)
	data = append(data, payload...)
	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(n+11))
	return append(data, prev[:]...)
}
func fillerFixture(o fillerFixtureOptions) []byte {
	length := o.nalLength
	if length == 0 {
		length = 4
	}
	data := []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}
	metadata := []byte("metadata")
	if o.mutation == "large-metadata" {
		metadata = bytes.Repeat([]byte("m"), 4096)
	}
	if o.mutation == "metadata" {
		metadata = []byte("rebuilt keyframe index")
	}
	data = appendFixtureTag(data, 18, 0, metadata)
	config := []byte{1, 100, 0, 31, 0xfc | byte(length-1), 0xe1, 0, 4, 0x67, 100, 0, 31, 1, 0, 2, 0x68, 1}
	if o.mutation == "config" {
		config[len(config)-1] ^= 1
	}
	data = appendFixtureTag(data, 9, 0, append([]byte{0x17, 0, 0, 0, 0}, config...))
	data = appendFixtureTag(data, 8, 0, []byte{0xaf, 0, 0x12, 0x10})
	for i := 0; i < 10; i++ {
		timestamp := uint32(i * 1000)
		audio := []byte{0xaf, 1, 42, byte(i)}
		if o.mutation == "audio" && i == 3 {
			audio[2] ^= 1
		}
		data = appendFixtureTag(data, 8, timestamp, audio)
		if o.mutation == "drop" && i == 4 {
			continue
		}
		video := []byte{0x27, 1, 0, 0, 0}
		if o.mutation == "cts" && i == 3 {
			video[4] = 1
		}
		if o.mutation == "codec" {
			video[0] = 0x2c
		}
		picture := []byte{0x41, byte(i), 0x88}
		if o.mutation == "picture" && i == 3 {
			picture[2] ^= 1
		}
		addNAL := func(nal []byte) {
			var prefix [4]byte
			binary.BigEndian.PutUint32(prefix[:], uint32(len(nal)))
			video = append(video, prefix[4-length:]...)
			video = append(video, nal...)
		}
		addNAL(picture)
		if o.filler {
			filler := append([]byte{12}, bytes.Repeat([]byte{0xff}, 120)...)
			filler = append(filler, 0x80)
			if o.mutation == "invalid-filler" {
				filler[10] = 0
			}
			if o.mutation == "reference-filler" {
				filler[0] = 0x2c
			}
			addNAL(filler)
		}
		if o.mutation == "bad-length" && i == 3 {
			for j := 0; j < length; j++ {
				video[5+j] = 0
			}
		}
		if o.mutation == "timestamp" && i == 3 {
			timestamp++
		}
		data = appendFixtureTag(data, 9, timestamp, video)
	}
	return appendFixtureTag(data, 9, 9000, []byte{0x17, 2, 0, 0, 0})
}
func writeFillerFixture(t *testing.T, name string, o fillerFixtureOptions) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, fillerFixture(o), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestFillerOnlyVerification(t *testing.T) {
	for _, length := range []int{1, 2, 4} {
		t.Run(string(rune('0'+length)), func(t *testing.T) {
			source := writeFillerFixture(t, "source.flv", fillerFixtureOptions{filler: true, nalLength: length})
			output := writeFillerFixture(t, "output.flv", fillerFixtureOptions{nalLength: length, mutation: "metadata"})
			evidence, err := verifyH264FillerRemoval(context.Background(), source, output)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.RemovedUnits != 10 || evidence.RemovedBytes != int64(10*(122+length)) {
				t.Fatalf("%+v", evidence)
			}
		})
	}
}
func TestFillerVerificationRejectsOtherChanges(t *testing.T) {
	for _, mutation := range []string{"audio", "picture", "config", "cts", "timestamp", "drop", "codec", "bad-length", "invalid-filler", "reference-filler", "no-reduction"} {
		t.Run(mutation, func(t *testing.T) {
			original := fillerFixtureOptions{filler: true}
			output := fillerFixtureOptions{mutation: mutation}
			if mutation == "invalid-filler" || mutation == "reference-filler" || mutation == "bad-length" {
				original.mutation = mutation
				output.mutation = ""
			}
			if mutation == "no-reduction" {
				output.filler = true
			}
			source := writeFillerFixture(t, "source.flv", original)
			dest := writeFillerFixture(t, "dest.flv", output)
			if _, err := verifyH264FillerRemoval(context.Background(), source, dest); err == nil {
				t.Fatal("unsafe shrink accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := writeFillerFixture(t, "source.flv", fillerFixtureOptions{filler: true})
	if _, err := verifyH264FillerRemoval(ctx, source, source); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestRepairFillerShrinkIntegration(t *testing.T) {
	for _, mode := range []string{"filler-only", "filler-audio", "filler-picture", "filler-config", "filler-timestamp", "filler-cts", "filler-drop", "filler-metadata"} {
		t.Run(mode, func(t *testing.T) {
			original := fillerFixture(fillerFixtureOptions{filler: true})
			path := filepath.Join(t.TempDir(), "input.flv")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := repairFLV(context.Background(), path, nil, func(in, dest string) (*exec.Cmd, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestRepairHelper$", "--", in, dest, mode)
				cmd.Env = append(os.Environ(), "FLV_REPAIR_HELPER=1")
				return cmd, nil
			})
			accepted := mode == "filler-only" || mode == "filler-metadata"
			if (err == nil) != accepted {
				t.Fatalf("out=%v err=%v", out, err)
			}
			after, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if !accepted && !bytes.Equal(after, original) {
				t.Fatal("original changed after rejection")
			}
			if accepted && bytes.Equal(after, original) {
				t.Fatal("repaired file not adopted")
			}
			records, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".bililive-fix-*", "filler-verification.json"))
			if accepted && len(records) != 1 {
				t.Fatal("missing verification evidence")
			}
		})
	}
}

func TestFillerCannotHideOtherSizeReduction(t *testing.T) {
	source := writeFillerFixture(t, "source.flv", fillerFixtureOptions{filler: true, mutation: "large-metadata"})
	output := writeFillerFixture(t, "output.flv", fillerFixtureOptions{})
	if _, err := verifyH264FillerRemoval(context.Background(), source, output); err == nil {
		t.Fatal("unexplained metadata shrink accepted")
	}
}
