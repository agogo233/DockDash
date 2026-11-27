package registry

import "regexp"

type ParsedTag struct {
	Version string
	Prefix  string
	Suffix  string
	Parts   []int
}

var semverPattern = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?(?:\.\d+)?)`)

func ExtractSemVer(tag string) *ParsedTag {
	indexes := semverPattern.FindStringSubmatchIndex(tag)
	if indexes == nil {
		return nil
	}
	version := tag[indexes[2]:indexes[3]]
	parts := []int{}
	for _, part := range splitDots(version) {
		value := 0
		for _, char := range part {
			if char < '0' || char > '9' {
				return nil
			}
			value = value*10 + int(char-'0')
		}
		parts = append(parts, value)
	}
	return &ParsedTag{
		Version: version,
		Prefix:  tag[:indexes[0]],
		Suffix:  tag[indexes[1]:],
		Parts:   parts,
	}
}

func splitDots(value string) []string {
	result := []string{}
	current := ""
	for _, char := range value {
		if char == '.' {
			result = append(result, current)
			current = ""
			continue
		}
		current += string(char)
	}
	return append(result, current)
}

func prefixMatches(a, b string) bool {
	if a == "v" {
		a = ""
	}
	if b == "v" {
		b = ""
	}
	return a == b
}

func SameFamily(a, b *ParsedTag) bool {
	return a != nil && b != nil && prefixMatches(a.Prefix, b.Prefix) && a.Suffix == b.Suffix
}

func CompareSemVer(a, b *ParsedTag) int {
	if a == nil || b == nil {
		return 0
	}
	length := len(a.Parts)
	if len(b.Parts) > length {
		length = len(b.Parts)
	}
	for i := 0; i < length; i++ {
		left, right := 0, 0
		if i < len(a.Parts) {
			left = a.Parts[i]
		}
		if i < len(b.Parts) {
			right = b.Parts[i]
		}
		if left != right {
			if left > right {
				return 1
			}
			return -1
		}
	}
	return 0
}

func ContainsUpdate(tags []string, runningTag string) bool {
	current := ExtractSemVer(runningTag)
	if current == nil {
		return false
	}
	for _, tag := range tags {
		candidate := ExtractSemVer(tag)
		if SameFamily(candidate, current) && CompareSemVer(candidate, current) > 0 {
			return true
		}
	}
	return false
}
