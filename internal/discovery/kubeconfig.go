package discovery

import (
	"bufio"
	"strings"
)

type kubeconfigEntry struct {
	name   string
	fields map[string]string
}

type kubeconfigData struct {
	clusters       []kubeconfigEntry
	users          []kubeconfigEntry
	contexts       []kubeconfigEntry
	currentContext string
}

type yamlKey struct {
	indent int
	key    string
}

func parseKubeconfig(content []byte) kubeconfigData {
	data := kubeconfigData{}
	section := ""
	current := -1
	stack := []yamlKey{}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && !strings.HasPrefix(trimmed, "-") && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSuffix(trimmed, ":")
			stack = nil
			if section != "clusters" && section != "users" && section != "contexts" {
				section = ""
			}
			continue
		}
		key, value, ok := splitYAMLKey(trimmed)
		if !ok {
			continue
		}
		if indent == 0 && key == "current-context" {
			data.currentContext = yamlScalar(value)
			continue
		}
		if section == "" {
			continue
		}
		entries := kubeconfigSection(&data, section)
		if strings.HasPrefix(trimmed, "-") {
			current = len(*entries)
			*entries = append(*entries, kubeconfigEntry{fields: map[string]string{}})
			stack = nil
			itemKey, itemValue, ok := splitYAMLKey(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
			if ok {
				(*entries)[current].fields[itemKey] = yamlScalar(itemValue)
				if itemKey == "name" {
					(*entries)[current].name = yamlScalar(itemValue)
				}
				if itemValue == "" {
					stack = append(stack, yamlKey{indent: indent + 2, key: itemKey})
				}
			}
			continue
		}
		if current < 0 {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parts := make([]string, 0, len(stack)+1)
		for _, item := range stack {
			parts = append(parts, item.key)
		}
		parts = append(parts, key)
		path := strings.Join(parts, ".")
		(*entries)[current].fields[path] = yamlScalar(value)
		if path == "name" {
			(*entries)[current].name = yamlScalar(value)
		}
		if value == "" {
			stack = append(stack, yamlKey{indent: indent, key: key})
		}
	}
	return data
}

func kubeconfigSection(data *kubeconfigData, section string) *[]kubeconfigEntry {
	switch section {
	case "clusters":
		return &data.clusters
	case "users":
		return &data.users
	default:
		return &data.contexts
	}
}

func splitYAMLKey(line string) (string, string, bool) {
	separator := strings.Index(line, ":")
	if separator < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:separator]), stripYAMLComment(line[separator+1:]), true
}

func stripYAMLComment(value string) string {
	singleQuote := false
	doubleQuote := false
	for index, char := range value {
		switch char {
		case '\'':
			if !doubleQuote {
				singleQuote = !singleQuote
			}
		case '"':
			if !singleQuote {
				doubleQuote = !doubleQuote
			}
		case '#':
			if !singleQuote && !doubleQuote && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
				return value[:index]
			}
		}
	}
	return value
}

func yamlScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return value
}

func findKubeconfigEntry(entries []kubeconfigEntry, name string) (kubeconfigEntry, bool) {
	for _, entry := range entries {
		if entry.name == name {
			return entry, true
		}
	}
	return kubeconfigEntry{}, false
}
