package files

import (
	"strings"
	"unicode/utf8"
)

// maxNameLength is the longest file or folder name allowed (Windows' limit).
const maxNameLength = 255

// Hidden folders the server keeps inside each user's folder. Keeping them
// there, on the same drive as the files, makes finishing an upload or
// deleting a folder a single, instant rename.
const (
	uploadsDir = ".ashcloud-uploads" // uploads still arriving
	trashDir   = ".ashcloud-trash"   // deleted items, kept 30 days
)

// isReserved reports names the server keeps for itself (Windows ignores
// case, so this does too).
func isReserved(name string) bool {
	return strings.EqualFold(name, uploadsDir) || strings.EqualFold(name, trashDir)
}

// Clean checks a path from the browser, such as "/Photos/2024/beach.jpg",
// and returns it in canonical form ("/Photos/2024/beach.jpg", "/" for the
// top folder) along with the matching path relative to the user's folder
// ("Photos/2024/beach.jpg", "." for the top folder).
//
// Paths use "/" only. ".", ".." and names Windows can't store are refused,
// so a path always means exactly one place inside the user's folder.
func Clean(path string) (canonical, relative string, err error) {
	if !utf8.ValidString(path) || strings.ContainsAny(path, "\\\x00") {
		return "", "", ErrBadPath
	}
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue // leading, trailing or doubled slashes
		}
		if !ValidName(part) || isReserved(part) {
			return "", "", ErrBadPath
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "/", ".", nil
	}
	return "/" + strings.Join(parts, "/"), strings.Join(parts, "/"), nil
}

// ValidName reports whether name can be a file or folder name on Windows:
// not "." or "..", no < > : " / \ | ? * or control characters, no trailing
// dot or space, not a reserved device name like CON or NUL, at most 255
// characters. (A ":" would otherwise reach a hidden NTFS data stream.)
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > maxNameLength {
		return false
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	if last := name[len(name)-1]; last == '.' || last == ' ' {
		return false
	}
	return !isReservedName(name)
}

// isReservedName reports names Windows treats as devices, with or without an
// extension: CON, PRN, AUX, NUL, COM0-9, LPT0-9 (and the superscript-digit
// variants such as COM¹).
func isReservedName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, device := range []string{"COM", "LPT"} {
		if digit, ok := strings.CutPrefix(base, device); ok &&
			utf8.RuneCountInString(digit) == 1 && strings.Contains("0123456789¹²³", digit) {
			return true
		}
	}
	return false
}
