package install

import (
	"encoding/binary"
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const lxSymlinkReparseTag uint32 = 0xA000001D

func parseLXReparseTarget(raw []byte) (string, bool, error) {
	if len(raw) < 8 {
		return "", false, errors.New("truncated reparse header")
	}
	tag := binary.LittleEndian.Uint32(raw[:4])
	if tag != lxSymlinkReparseTag {
		return "", false, nil
	}
	dataLen := int(binary.LittleEndian.Uint16(raw[4:6]))
	if dataLen < 5 || dataLen != len(raw)-8 {
		return "", true, errors.New("invalid LX reparse payload length")
	}
	payload := raw[8 : 8+dataLen]
	if binary.LittleEndian.Uint32(payload[:4]) != 2 {
		return "", true, errors.New("unsupported LX symlink payload version")
	}
	pathBytes := payload[4:]
	if len(pathBytes) < 2 || pathBytes[len(pathBytes)-1] != 0 || strings.IndexByte(string(pathBytes[:len(pathBytes)-1]), 0) >= 0 || !utf8.Valid(pathBytes[:len(pathBytes)-1]) {
		return "", true, errors.New("malformed LX symlink target")
	}
	target := string(pathBytes[:len(pathBytes)-1])
	if !strings.HasPrefix(target, "/") || strings.Contains(target, "\\") || path.Clean(target) != target {
		return "", true, errors.New("invalid LX symlink target path")
	}
	return target, true, nil
}
