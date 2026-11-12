package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetPrinters memperbarui printers.allowlist dan printers.defaultName pada file
// YAML di path. Implementasi berbasis yaml.Node sehingga komentar dan field lain
// di dalam file tetap terjaga. Fungsi ini portabel untuk semua OS.
func SetPrinters(path string, allowlist []string, defaultName string) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}

	root, err := loadYAMLNode(path)
	if err != nil {
		return err
	}
	doc := root.Content[0]

	printers := findOrCreateMapKey(doc, "printers")
	setScalar(printers, "defaultName", defaultName)
	setSeq(printers, "allowlist", allowlist)

	return writeYAMLNode(path, root)
}

// LoadPrinters membaca hanya bagian printers dari file config tanpa efek
// samping (tidak menulis ./data).
func LoadPrinters(path string) (PrintersConfig, error) {
	var doc struct {
		Printers PrintersConfig `yaml:"printers"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PrintersConfig{}, nil
		}
		return PrintersConfig{}, fmt.Errorf("read config file failed: %w", err)
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return PrintersConfig{}, fmt.Errorf("parse config yaml failed: %w", err)
	}
	return doc.Printers, nil
}

// SetPrintersConfig mengganti seluruh bagian printers (defaultName, allowlist,
// thermal) sekaligus, mempertahankan field & komentar lain di file.
func SetPrintersConfig(path string, cfg PrintersConfig) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	root, err := loadYAMLNode(path)
	if err != nil {
		return err
	}
	doc := root.Content[0]

	printers := findOrCreateMapKey(doc, "printers")
	setScalar(printers, "defaultName", cfg.DefaultName)
	setSeq(printers, "allowlist", cfg.Allowlist)
	setThermalSeq(printers, cfg.Thermal)

	return writeYAMLNode(path, root)
}

func setThermalSeq(m *yaml.Node, list []ThermalPrinterConfig) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if len(list) == 0 {
		seq.Style = yaml.FlowStyle
	}
	for _, t := range list {
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addScalar(entry, "name", "!!str", t.Name)
		if t.WidthMm > 0 {
			addScalar(entry, "widthMm", "!!int", fmt.Sprintf("%d", t.WidthMm))
		}
		if t.DPI > 0 {
			addScalar(entry, "dpi", "!!int", fmt.Sprintf("%d", t.DPI))
		}
		if t.Cut != nil {
			addScalar(entry, "cut", "!!bool", fmt.Sprintf("%t", *t.Cut))
		}
		if t.Trim != nil {
			addScalar(entry, "trim", "!!bool", fmt.Sprintf("%t", *t.Trim))
		}
		if t.Feed > 0 {
			addScalar(entry, "feed", "!!int", fmt.Sprintf("%d", t.Feed))
		}
		seq.Content = append(seq.Content, entry)
	}

	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "thermal" {
			m.Content[i+1] = seq
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "thermal"},
		seq,
	)
}

func addScalar(m *yaml.Node, key, tag, value string) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

// EnsureThermalPrinters memastikan setiap nama printer ada di dalam daftar
// printers.thermal tanpa mengubah entri yang sudah ada (mempertahankan widthMm,
// dpi, cut, serta komentar). Entri baru dibuat minimal (name saja) sehingga
// memakai nilai default resolver.
func EnsureThermalPrinters(path string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if path == "" {
		return fmt.Errorf("config path is empty")
	}

	root, err := loadYAMLNode(path)
	if err != nil {
		return err
	}
	doc := root.Content[0]

	printers := findOrCreateMapKey(doc, "printers")
	seq := findOrCreateSeqKey(printers, "thermal")

	existing := make(map[string]bool)
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "name" {
				existing[strings.ToLower(strings.TrimSpace(item.Content[i+1].Value))] = true
			}
		}
	}

	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || existing[strings.ToLower(n)] {
			continue
		}
		entry := &yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: n},
			},
		}
		seq.Content = append(seq.Content, entry)
		existing[strings.ToLower(n)] = true
	}

	return writeYAMLNode(path, root)
}

func loadYAMLNode(path string) (*yaml.Node, error) {
	var root yaml.Node
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &root); err != nil {
			return nil, fmt.Errorf("parse config yaml failed: %w", err)
		}
		if root.Kind == 0 {
			root = yaml.Node{Kind: yaml.DocumentNode}
		}
	case os.IsNotExist(err):
		root = yaml.Node{Kind: yaml.DocumentNode}
	default:
		return nil, fmt.Errorf("read config file failed: %w", err)
	}

	if len(root.Content) == 0 {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}
	return &root, nil
}

func writeYAMLNode(path string, root *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("marshal config yaml failed: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("marshal config yaml failed: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write config file failed: %w", err)
	}
	return nil
}

func findOrCreateMapKey(m *yaml.Node, key string) *yaml.Node {
	if m.Kind == 0 {
		m.Kind = yaml.MappingNode
		m.Tag = "!!map"
	}
	if m.Kind != yaml.MappingNode {
		// Ganti node non-mapping menjadi mapping kosong agar struktur valid.
		*m = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, k, v)
	return v
}

func findOrCreateSeqKey(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		*m = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			seq := m.Content[i+1]
			if seq.Kind != yaml.SequenceNode {
				seq.Kind = yaml.SequenceNode
				seq.Tag = "!!seq"
				seq.Content = nil
				seq.Value = ""
			}
			return seq
		}
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
	return seq
}

func setScalar(m *yaml.Node, key, value string) {
	if m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			n := m.Content[i+1]
			style := n.Style
			*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style}
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func setSeq(m *yaml.Node, key string, values []string) {
	if m.Kind != yaml.MappingNode {
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if len(values) == 0 {
		seq.Style = yaml.FlowStyle
	}
	for _, v := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			comment := m.Content[i+1].LineComment
			seq.LineComment = comment
			m.Content[i+1] = seq
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
}
