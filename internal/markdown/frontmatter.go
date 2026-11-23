package markdown

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// PublicationFrontMatter contains only the top-level metadata fields eligible
// for publication. Date is zero if there is no supported date.
type PublicationFrontMatter struct {
	Title      string
	Date       time.Time
	Tags       []string
	Categories []string
}

// ExtractPublicationFrontMatter is intentionally separate from Extract, whose
// vault-wide tags include inline tags and nested properties. Do not evaluate
// aliases or merge keys: those could make private properties public indirectly.
func ExtractPublicationFrontMatter(raw []byte) PublicationFrontMatter {
	_, properties := splitFrontmatter(normalize(raw))
	if properties == "" {
		return PublicationFrontMatter{}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(properties), &doc); err != nil || len(doc.Content) != 1 {
		return PublicationFrontMatter{}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return PublicationFrontMatter{}
	}

	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			continue
		}
		switch key.Value {
		case "title", "date", "tags", "categories":
			if _, duplicate := fields[key.Value]; duplicate {
				fields[key.Value] = nil // Ambiguous fields are ignored, not resolved by order.
			} else {
				fields[key.Value] = root.Content[i+1]
			}
		}
	}
	return PublicationFrontMatter{
		Title:      publicationLabel(fields["title"]),
		Date:       publicationDate(fields["date"]),
		Tags:       publicationLabels(fields["tags"], true),
		Categories: publicationLabels(fields["categories"], false),
	}
}

func publicationLabel(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	// Keep ordinary YAML scalars (including numeric labels like 2025) as
	// display text, never as typed expressions or HTML. Nulls are not labels.
	switch n.Tag {
	case "!!str", "!!int", "!!float", "!!bool", "!!timestamp":
		label := strings.Join(strings.Fields(n.Value), " ")
		if !strings.ContainsFunc(label, unicode.IsControl) {
			return label
		}
		return ""
	default:
		return ""
	}
}

const (
	maxPublicationLabelBytes = 256 // Must fit in a public archive filter.
	maxPublicationLabels     = 64
)

func publicationLabels(n *yaml.Node, tag bool) []string {
	if n == nil {
		return nil
	}
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		label := publicationLabel(item)
		if tag {
			label = strings.TrimSpace(strings.TrimPrefix(label, "#"))
		}
		if label == "" || len(label) > maxPublicationLabelBytes {
			continue
		}
		key := strings.ToLower(label)
		if !seen[key] {
			out = append(out, label)
			seen[key] = true
			if len(out) == maxPublicationLabels {
				break
			}
		}
	}
	return out
}

// The public date formatter uses Beijing time (UTC+08:00), including for
// historical dates. Interpret a date without a time in that same zone.
var publicationDateZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// time.Parse accepts a few non-RFC3339 variants; require the zoned syntax.
var zonedPublicationTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func publicationDate(n *yaml.Node) time.Time {
	if n == nil || n.Kind != yaml.ScalarNode || (n.Tag != "!!str" && n.Tag != "!!timestamp") {
		return time.Time{}
	}
	value := strings.TrimSpace(n.Value)
	var t time.Time
	var err error
	if zonedPublicationTime.MatchString(value) {
		t, err = time.Parse(time.RFC3339, value)
	} else {
		// YAML dates may use an unpadded month or day; unzoned times may not.
		t, err = time.ParseInLocation("2006-1-2", value, publicationDateZone)
	}
	if err != nil || t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}
	}
	t = t.UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}
	}
	return t
}
