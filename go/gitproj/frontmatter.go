package gitproj

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// frontmatterDelim is the fence that opens and closes the YAML block, exactly
// as every static-site generator and most markdown tooling expects it.
const frontmatterDelim = "---"

// RenderFrontmatter writes the deterministic `---\n<yaml>\n---\n\n<body>`
// format. The same values and body always produce byte-identical output:
// map keys are sorted (alphabetically, at every nesting level) rather than
// emitted in Go's randomised map iteration order, because the projection
// worker decides whether a commit is needed by byte-comparing this output
// against the git tree — a non-deterministic Render would mean every
// rendering created a spurious commit.
//
// values may hold strings, bools, ints, []string, and nested
// map[string]interface{} (a worker's mcp_config is a jsonb map; a skill's
// briefing is a list of selectors). Any other type is a bug at the call
// site, not a data problem, so it is returned as an error rather than
// silently stringified.
//
// An empty values map still renders a frontmatter block (`---\n---\n\n`) so
// that Parse and Render round-trip: a file this package writes is always a
// file this package (or a human) can read back unambiguously as
// "frontmatter present, just empty" rather than "no frontmatter".
func RenderFrontmatter(values map[string]interface{}, body string) ([]byte, error) {
	node, err := mappingNode(values)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("gitproj: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("gitproj: encode frontmatter: %w", err)
	}

	var out bytes.Buffer
	out.WriteString(frontmatterDelim)
	out.WriteByte('\n')
	out.Write(buf.Bytes())
	out.WriteString(frontmatterDelim)
	out.WriteString("\n\n")
	out.WriteString(body)
	return out.Bytes(), nil
}

// mappingNode builds a yaml Mapping node whose keys are in sorted order at
// every level, so the emitted document does not depend on Go's map
// iteration order or on any library-internal sort that could change across
// yaml.v3 versions.
func mappingNode(values map[string]interface{}) (*yaml.Node, error) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range keys {
		keyNode := &yaml.Node{}
		if err := keyNode.Encode(k); err != nil {
			return nil, fmt.Errorf("gitproj: encode key %q: %w", k, err)
		}
		valNode, err := valueNode(values[k])
		if err != nil {
			return nil, fmt.Errorf("gitproj: key %q: %w", k, err)
		}
		node.Content = append(node.Content, keyNode, valNode)
	}
	return node, nil
}

// valueNode encodes one frontmatter value. Nested maps recurse through
// mappingNode so their keys are sorted too; everything else is handed to
// yaml.Node.Encode, which is safe for the remaining supported leaf types
// (string, bool, int, []string) because none of them contain a nested map
// whose key order would need pinning.
func valueNode(v interface{}) (*yaml.Node, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		return mappingNode(val)
	case string, bool, int, []string:
		node := &yaml.Node{}
		if err := node.Encode(val); err != nil {
			return nil, fmt.Errorf("encode value: %w", err)
		}
		return node, nil
	default:
		return nil, fmt.Errorf("unsupported frontmatter value type %T", v)
	}
}

// ParseFrontmatter reads the format RenderFrontmatter writes, but tolerates
// hand-edited variations (different indent width, quoting, key order — see
// §C of the design doc: humans and the importer both read files this
// package did not write). A file with no opening `---` fence is not an
// error: it is a body-only file, and values comes back nil.
//
// Malformed frontmatter (an opening fence with no matching close, or YAML
// that does not parse to a mapping) IS an error naming the problem, because
// the importer's quarantine path (§C) needs to say why a file was rejected.
func ParseFrontmatter(data []byte) (values map[string]interface{}, body string, err error) {
	text := string(data)

	if !strings.HasPrefix(text, frontmatterDelim+"\n") && text != frontmatterDelim {
		return nil, text, nil
	}

	rest := strings.TrimPrefix(text, frontmatterDelim+"\n")
	closeIdx := findClosingFence(rest)
	if closeIdx < 0 {
		return nil, "", fmt.Errorf("gitproj: unterminated frontmatter (no closing %q)", frontmatterDelim)
	}

	yamlBlock := rest[:closeIdx]
	after := rest[closeIdx+len(frontmatterDelim):]
	after = strings.TrimPrefix(after, "\n")
	after = strings.TrimPrefix(after, "\n")

	if strings.TrimSpace(yamlBlock) == "" {
		return map[string]interface{}{}, after, nil
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlBlock), &raw); err != nil {
		return nil, "", fmt.Errorf("gitproj: malformed frontmatter: %w", err)
	}
	return normalize(raw), after, nil
}

// findClosingFence returns the byte offset of the closing "---" line (the
// fence itself must occupy its own line) or -1 if none is found.
func findClosingFence(s string) int {
	lines := strings.SplitAfter(s, "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == frontmatterDelim {
			return offset
		}
		offset += len(line)
	}
	return -1
}

// normalize walks a value tree produced by yaml.Unmarshal into
// map[string]interface{} and coerces it to the subset of types
// RenderFrontmatter understands: nested map[interface{}]interface{}/
// map[string]interface{} become map[string]interface{}, []interface{} of
// strings become []string, and scalars pass through. This is what lets a
// value parsed from a hand-edited file be handed straight back into
// RenderFrontmatter (e.g. by the importer re-rendering to check for drift)
// without a type-assertion panic.
func normalize(v interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(v.(map[string]interface{})))
	for k, val := range v.(map[string]interface{}) {
		out[k] = normalizeValue(val)
	}
	return out
}

func normalizeValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return normalize(val)
	case []interface{}:
		allStrings := true
		strs := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			strs[i] = s
		}
		if allStrings {
			return strs
		}
		normalized := make([]interface{}, len(val))
		for i, item := range val {
			normalized[i] = normalizeValue(item)
		}
		return normalized
	default:
		return val
	}
}
