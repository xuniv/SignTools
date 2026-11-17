package util

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"testing"

	"howett.net/plist"
)

func metadataExecutable(cryptid uint32) []byte {
	data := make([]byte, 72)
	for offset, value := range map[int]uint32{0: 0xfeedfacf, 4: 0x100000c, 12: 2, 16: 1, 20: 24, 32: 0x2c, 36: 24, 40: 56, 44: 16, 48: cryptid} {
		binary.LittleEndian.PutUint32(data[offset:], value)
	}
	return data
}

type metadataGuardReader struct {
	prefix    *bytes.Reader
	tailReads int
}

func (r *metadataGuardReader) Read(p []byte) (int, error) {
	if r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	r.tailReads++
	return 0, fmt.Errorf("unexpected payload read")
}

func TestFatMetadataRejectsHugeOffsetsBeforeReadingPayload(t *testing.T) {
	for _, offset := range []uint32{uint32(maxEncryptionScanBytes + 1), 0xf0000000} {
		header := make([]byte, 28)
		binary.BigEndian.PutUint32(header, 0xcafebabe)
		binary.BigEndian.PutUint32(header[4:], 1)
		binary.BigEndian.PutUint32(header[16:], offset)
		binary.BigEndian.PutUint32(header[20:], 72)
		guard := &metadataGuardReader{prefix: bytes.NewReader(header)}
		state, err := ReadMachOEncryption(guard)
		if err == nil || state != EncryptionUnknown || guard.tailReads != 0 {
			t.Fatalf("huge fat offset read payload: state%s err%v reads%d", state, err, guard.tailReads)
		}
	}
	// A short compressed member cannot claim a distant slice even when the
	// global scan budget would allow that offset.
	main := metadataArchive(t, plist.XMLFormat, "App", metadataExecutable(0))
	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header, 0xcafebabe)
	binary.BigEndian.PutUint32(header[4:], 1)
	binary.BigEndian.PutUint32(header[16:], 1<<20)
	binary.BigEndian.PutUint32(header[20:], 72)
	// Replace the original executable by constructing a fresh small ZIP.
	reader, err := zip.NewReader(bytes.NewReader(main), int64(len(main)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, entry := range reader.File {
		w, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "Payload/Test.app/Test" {
			_, err = w.Write(header)
		} else {
			r, openErr := entry.Open()
			if openErr != nil {
				t.Fatal(openErr)
			}
			_, err = io.Copy(w, r)
			_ = r.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := ReadAppMetadata(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Encryption != EncryptionUnknown {
		t.Fatal("impossible slice labelled unencrypted")
	}
}

func metadataExtraEntries(t *testing.T, ipa []byte, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	reader, err := zip.NewReader(bytes.NewReader(ipa), int64(len(ipa)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reader.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		w, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, r); err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
	}
	for name, data := range entries {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMetadataChecksEmbeddedExtensionsAndRejectsAmbiguity(t *testing.T) {
	main := metadataArchive(t, plist.XMLFormat, "App", metadataExecutable(0))
	extensionPlist, err := plist.Marshal(map[string]string{"CFBundleIdentifier": "com.example.extension", "CFBundleExecutable": "ExtensionBinary"}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	ipa := metadataExtraEntries(t, main, map[string][]byte{"Payload/Test.app/PlugIns/Thing.appex/Info.plist": extensionPlist, "Payload/Test.app/PlugIns/Thing.appex/ExtensionBinary": metadataExecutable(1)})
	info, err := ReadAppMetadata(bytes.NewReader(ipa), int64(len(ipa)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Encryption != EncryptionEncrypted {
		t.Fatalf("encrypted embedded extension ignored: %#v", info)
	}
	// A duplicate Info.plist can be interpreted differently by extractors; never
	// present the first entry as authoritative actual metadata.
	ipa = metadataExtraEntries(t, main, map[string][]byte{"Payload/Test.app/Info.plist": extensionPlist})
	if _, err := ReadAppMetadata(bytes.NewReader(ipa), int64(len(ipa))); err == nil {
		t.Fatal("ambiguous main Info.plist accepted")
	}
}

func metadataArchive(t *testing.T, format int, displayName string, executable []byte) []byte {
	t.Helper()
	data, err := plist.Marshal(map[string]any{"CFBundleIdentifier": "com.example.actual", "CFBundleDisplayName": displayName, "CFBundleName": "Fallback", "CFBundleShortVersionString": "3.2.1", "CFBundleVersion": "3217", "CFBundleExecutable": "Test"}, format)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, body := range map[string][]byte{"Payload/Test.app/Info.plist": data, "Payload/Test.app/Test": executable} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestActualMetadataSupportsBinaryAndXML(t *testing.T) {
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		for _, state := range []struct {
			cryptid uint32
			want    string
		}{{1, EncryptionEncrypted}, {0, EncryptionUnencrypted}} {
			data := metadataArchive(t, format, "Actual Signed Name", metadataExecutable(state.cryptid))
			info, err := ReadAppMetadata(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if info.BundleID != "com.example.actual" || info.DisplayName != "Actual Signed Name" || info.Version != "3.2.1" || info.BuildVersion != "3217" || info.Encryption != state.want {
				t.Fatalf("unexpected metadata: %#v", info)
			}
		}
	}
	data := metadataArchive(t, plist.XMLFormat, "", metadataExecutable(0))
	info, err := ReadAppMetadata(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if info.DisplayName != "Fallback" {
		t.Fatalf("bundle-name fallback missing: %#v", info)
	}
}

func TestMachOEncryptionDoesNotAssumePlaintext(t *testing.T) {
	noCommand := metadataExecutable(0)[:32]
	binary.LittleEndian.PutUint32(noCommand[16:], 0)
	binary.LittleEndian.PutUint32(noCommand[20:], 0)
	state, err := ReadMachOEncryption(bytes.NewReader(noCommand))
	if err != nil || state != EncryptionUnknown {
		t.Fatalf("absence of command labelled plaintext: %s %v", state, err)
	}
	for _, data := range [][]byte{[]byte("not Mach-O"), metadataExecutable(0)[:40]} {
		state, err := ReadMachOEncryption(bytes.NewReader(data))
		if err == nil || state != EncryptionUnknown {
			t.Fatalf("malformed executable accepted: %s %v", state, err)
		}
	}
}

func TestFatMachOChecksEverySlice(t *testing.T) {
	for _, second := range []uint32{0, 1} {
		data := make([]byte, 224)
		binary.BigEndian.PutUint32(data, 0xcafebabe)
		binary.BigEndian.PutUint32(data[4:], 2)
		for i, offset := range []uint32{64, 144} {
			entry := 8 + i*20
			binary.BigEndian.PutUint32(data[entry+8:], offset)
			binary.BigEndian.PutUint32(data[entry+12:], 72)
		}
		copy(data[64:], metadataExecutable(0))
		copy(data[144:], metadataExecutable(second))
		got, err := ReadMachOEncryption(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		want := EncryptionUnencrypted
		if second != 0 {
			want = EncryptionEncrypted
		}
		if got != want {
			t.Fatalf("fat second cryptid%d: got %s wanted %s", second, got, want)
		}
	}
}
