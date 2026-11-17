package util

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	EncryptionEncrypted   = "encrypted"
	EncryptionUnencrypted = "unencrypted"
	EncryptionUnknown     = "unknown"
	EncryptionDecrypted   = "decrypted"
	// Large thin binaries need only their load commands. Fat slices beyond the
	// bounded sequential scan remain unknown instead of forcing ZIP decompression.
	maxMetadataExecutableBytes = uint64(512 << 20)
	maxEncryptionScanBytes     = int64(64 << 20)
)

// ReadAppMetadata reads actual main-bundle plist values without extracting icons.
// It supports both binary and XML plists and never infers decryption from a source name.
func ReadAppMetadata(r io.ReaderAt, size int64) (*AppInfo, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open IPA: %w", err)
	}
	bundle, err := findBundle(zr.File)
	if err != nil {
		return nil, err
	}
	if bundle.plist == nil {
		return nil, fmt.Errorf("IPA main bundle has no Info.plist")
	}
	mainPlists := 0
	for _, entry := range zr.File {
		parts := strings.Split(entry.Name, "/")
		if len(parts) > 0 && parts[0] == "Payload" {
			parts = parts[1:]
		}
		if !bundle.mac && len(parts) == 2 && strings.HasSuffix(parts[0], ".app") && parts[1] == "Info.plist" {
			mainPlists++
		}
		if bundle.mac && len(parts) == 3 && strings.HasSuffix(parts[0], ".app") && parts[1] == "Contents" && parts[2] == "Info.plist" {
			mainPlists++
		}
	}
	if mainPlists != 1 {
		return nil, fmt.Errorf("IPA has ambiguous main-bundle metadata")
	}
	plist, err := readInfoPlist(bundle.plist)
	if err != nil {
		return nil, err
	}
	info := &AppInfo{Mac: bundle.mac, BundleID: strings.TrimSpace(plist.BundleID), DisplayName: plist.displayName(), Version: plist.version(), BuildVersion: strings.TrimSpace(plist.BuildVersion), Executable: strings.TrimSpace(plist.Executable)}
	info.Encryption = bundleEncryption(bundle, info.Executable)
	return info, nil
}

func bundleEncryption(bundle *appBundle, executable string) string {
	if executable == "" || path.Base(executable) != executable || executable == "." {
		return EncryptionUnknown
	}
	var main *zip.File
	if bundle.mac {
		// Native Mac executables are outside Contents/Resources, our icon root.
		return EncryptionUnknown
	}
	main = bundle.resources[executable]
	if main == nil {
		return EncryptionUnknown
	}
	budget := maxEncryptionScanBytes
	state, err := zipExecutableEncryption(main, &budget)
	if err != nil {
		return EncryptionUnknown
	}
	if state == EncryptionEncrypted {
		return state
	}
	// Embedded executables may have separate encryption regions. If any candidate
	// explicitly declares encryption, do not call the entire archive unencrypted.
	candidates := map[string]*zip.File{}
	for name, file := range bundle.resources {
		if strings.HasSuffix(name, ".dylib") || strings.Contains(name, ".framework/") && path.Base(name) == strings.TrimSuffix(path.Base(path.Dir(name)), ".framework") {
			candidates[name] = file
		}
		if strings.HasSuffix(name, "/Info.plist") && (strings.Contains(name, ".app/") || strings.Contains(name, ".appex/") || strings.Contains(name, ".framework/")) {
			if file.UncompressedSize64 > 1<<20 {
				state = EncryptionUnknown
				continue
			}
			child, err := readInfoPlist(file)
			if err != nil {
				state = EncryptionUnknown
				continue
			}
			if child.Executable != "" && path.Base(child.Executable) == child.Executable {
				binaryName := path.Join(path.Dir(name), child.Executable)
				if binary := bundle.resources[binaryName]; binary != nil {
					candidates[binaryName] = binary
				}
			}
		}
	}
	for _, file := range candidates {
		if file == main {
			continue
		}
		child, readErr := zipExecutableEncryption(file, &budget)
		if readErr == nil && child == EncryptionEncrypted {
			return EncryptionEncrypted
		}
		if readErr != nil {
			state = EncryptionUnknown
		}
	}
	return state
}

func zipExecutableEncryption(file *zip.File, budget *int64) (string, error) {
	if file.UncompressedSize64 > maxMetadataExecutableBytes || *budget <= 0 {
		return EncryptionUnknown, fmt.Errorf("executable exceeds metadata scan limits")
	}
	rc, err := file.Open()
	if err != nil {
		return EncryptionUnknown, err
	}
	defer rc.Close()
	limit := min(*budget, int64(file.UncompressedSize64))
	probe := &io.LimitedReader{R: rc, N: limit}
	state, err := readMachOEncryption(probe, file.UncompressedSize64, uint64(limit))
	*budget -= limit - probe.N
	return state, err
}

// ReadMachOEncryption examines LC_ENCRYPTION_INFO / LC_ENCRYPTION_INFO_64 in
// every fat slice using bounded headers. No encryption command means unknown;
// unencrypted is returned only when every slice explicitly has cryptid == 0.
func ReadMachOEncryption(r io.Reader) (string, error) {
	probe := &io.LimitedReader{R: r, N: maxEncryptionScanBytes}
	return readMachOEncryption(probe, maxMetadataExecutableBytes, uint64(maxEncryptionScanBytes))
}

func readMachOEncryption(r io.Reader, advertisedSize, scanLimit uint64) (string, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return EncryptionUnknown, err
	}
	value := binary.BigEndian.Uint32(magic[:])
	var order binary.ByteOrder = binary.BigEndian
	fat64 := false
	switch value {
	case 0xcafebabe, 0xcafebabf, 0xbebafeca, 0xbfbafeca:
		fat64 = value == 0xcafebabf || value == 0xbfbafeca
		if value == 0xbebafeca || value == 0xbfbafeca {
			order = binary.LittleEndian
		}
	default:
		return thinEncryption(io.MultiReader(strings.NewReader(string(magic[:])), r))
	}
	var count [4]byte
	if _, err := io.ReadFull(r, count[:]); err != nil {
		return EncryptionUnknown, err
	}
	n := order.Uint32(count[:])
	if n == 0 || n > 64 {
		return EncryptionUnknown, fmt.Errorf("invalid fat architecture count")
	}
	entrySize := 20
	if fat64 {
		entrySize = 32
	}
	table := make([]byte, int(n)*entrySize)
	if _, err := io.ReadFull(r, table); err != nil {
		return EncryptionUnknown, err
	}
	type slice struct{ offset, size uint64 }
	slices := make([]slice, 0, n)
	for i := range n {
		entry := table[int(i)*entrySize:]
		var s slice
		if fat64 {
			s = slice{order.Uint64(entry[8:]), order.Uint64(entry[16:])}
		} else {
			s = slice{uint64(order.Uint32(entry[8:])), uint64(order.Uint32(entry[12:]))}
		}
		if s.size < 28 || s.offset+s.size < s.offset || s.offset+s.size > advertisedSize || s.offset > scanLimit || scanLimit-s.offset < 28 {
			return EncryptionUnknown, fmt.Errorf("invalid fat slice")
		}
		slices = append(slices, s)
	}
	sort.Slice(slices, func(i, j int) bool { return slices[i].offset < slices[j].offset })
	position := uint64(8 + len(table))
	previousEnd := position
	result := EncryptionUnencrypted
	for _, s := range slices {
		if s.offset < previousEnd {
			return EncryptionUnknown, fmt.Errorf("overlapping fat slice")
		}
		previousEnd = s.offset + s.size
		if _, err := io.CopyN(io.Discard, r, int64(s.offset-position)); err != nil {
			return EncryptionUnknown, err
		}
		limited := &io.LimitedReader{R: r, N: int64(s.size)}
		state, err := thinEncryption(limited)
		if err != nil {
			return EncryptionUnknown, err
		}
		if state == EncryptionEncrypted {
			return state, nil
		}
		if state == EncryptionUnknown {
			result = state
		}
		consumed := s.size - uint64(limited.N)
		position = s.offset + consumed
	}
	return result, nil
}

func thinEncryption(r io.Reader) (string, error) {
	var header [28]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return EncryptionUnknown, err
	}
	var order binary.ByteOrder
	is64 := false
	switch binary.BigEndian.Uint32(header[:4]) {
	case 0xfeedface:
		order = binary.BigEndian
	case 0xcefaedfe:
		order = binary.LittleEndian
	case 0xfeedfacf:
		order = binary.BigEndian
		is64 = true
	case 0xcffaedfe:
		order = binary.LittleEndian
		is64 = true
	default:
		return EncryptionUnknown, fmt.Errorf("not Mach-O")
	}
	if is64 {
		var reserved [4]byte
		if _, err := io.ReadFull(r, reserved[:]); err != nil {
			return EncryptionUnknown, err
		}
	}
	ncmds, size := order.Uint32(header[16:]), order.Uint32(header[20:])
	if ncmds > 65536 || size > 4<<20 || uint64(ncmds)*8 > uint64(size) {
		return EncryptionUnknown, fmt.Errorf("invalid Mach-O load commands")
	}
	commands := make([]byte, size)
	if _, err := io.ReadFull(r, commands); err != nil {
		return EncryptionUnknown, err
	}
	state := EncryptionUnknown
	for range ncmds {
		if len(commands) < 8 {
			return EncryptionUnknown, fmt.Errorf("truncated Mach-O command")
		}
		command, length := order.Uint32(commands), order.Uint32(commands[4:])
		if length < 8 || uint64(length) > uint64(len(commands)) {
			return EncryptionUnknown, fmt.Errorf("invalid Mach-O command length")
		}
		if command == 0x21 || command == 0x2c {
			minimum := uint32(20)
			if command == 0x2c {
				minimum = 24
			}
			if length < minimum {
				return EncryptionUnknown, fmt.Errorf("truncated encryption command")
			}
			if order.Uint32(commands[16:]) != 0 {
				return EncryptionEncrypted, nil
			}
			state = EncryptionUnencrypted
		}
		commands = commands[length:]
	}
	return state, nil
}
