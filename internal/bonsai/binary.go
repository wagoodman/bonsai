package bonsai

import (
	"debug/buildinfo"
	"debug/elf"
	"debug/gosym"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"sort"
	"strings"
)

// binaryInfo is the per-package size attribution of a compiled Go binary.
//
// When the binary carries a symbol table (an unstripped build), attribution is exact
// for executable code and all *named* data (rodata/data globals): every symbol's bytes
// are assigned to its package. The gopclntab function-metadata section is distributed
// across packages in proportion to their code size. Anonymous read-only data (pooled
// string/byte constants — notably protobuf descriptors) has no symbol and lands in the
// <generated> bucket or an adjacent symbol; recovering it per-package would require a
// disassembly pass, which this tool does not do.
//
// When the binary is stripped (no symbol table), only the executable code can be
// attributed, via the gopclntab function table (debug/gosym); data is left unattributed.
type binaryInfo struct {
	FileSize     uint64            // size of the analyzed file on disk
	SectionsSize uint64            // sum of file-backed, non-debug sections (~ the stripped binary size)
	CodeSize     uint64            // executable code (text)
	DataSize     uint64            // named data (rodata, data, ...)
	PclntabSize  uint64            // gopclntab metadata (distributed proportionally)
	SelfSize     map[string]uint64 // import path -> total attributed bytes (code + data + pclntab share)
	CodeSelfSize map[string]uint64 // import path -> code bytes only
	Sections     []SectionInfo
	Stripped     bool
	GOOS         string
	GOARCH       string
	MainPkgPath  string
	MainModule   string
}

// SectionInfo is a single file-backed section of the analyzed binary.
type SectionInfo struct {
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

// binSection / binSymbol are the format-agnostic views the attribution logic consumes.
type binSection struct {
	name             string
	addr, size       uint64
	fileBacked       bool
	isText, isPclntb bool
}

// addr in binSymbol is relative to the start of its section, so the delta-fill logic is
// uniform across Mach-O/ELF (absolute VAs) and PE (section-relative offsets).
type binSymbol struct {
	name string
	addr uint64
	sect int // index into the binSection slice, or -1
}

type binFormat interface {
	sections() []binSection
	symbols() []binSymbol
	textStart() uint64
	pclntab() ([]byte, error)
	close() error
}

func loadBinary(path string) (*binaryInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	bf, err := openBinFormat(path)
	if err != nil {
		return nil, err
	}
	defer bf.close()

	info := &binaryInfo{
		FileSize:     uint64(st.Size()),
		SelfSize:     map[string]uint64{},
		CodeSelfSize: map[string]uint64{},
	}
	if bi, err := buildinfo.ReadFile(path); err == nil {
		info.MainPkgPath = bi.Path
		info.MainModule = bi.Main.Path
		for _, s := range bi.Settings {
			switch s.Key {
			case "GOOS":
				info.GOOS = s.Value
			case "GOARCH":
				info.GOARCH = s.Value
			}
		}
	}

	secs := bf.sections()
	for _, s := range secs {
		if !s.fileBacked || isDebugSection(s.name) {
			continue
		}
		info.Sections = append(info.Sections, SectionInfo{s.name, s.size})
		info.SectionsSize += s.size
		if s.isPclntb {
			info.PclntabSize = s.size
		}
	}

	syms := bf.symbols()
	if countAttributable(syms) < 500 {
		// stripped binary: fall back to code-only attribution via gopclntab.
		info.Stripped = true
		if err := attributeStripped(bf, info); err != nil {
			return nil, err
		}
		return info, nil
	}
	attributeFromSymbols(secs, syms, info)
	return info, nil
}

// attributeFromSymbols assigns each symbol's bytes (computed by intra-section address
// delta) to its package, then distributes the gopclntab section proportionally to code.
func attributeFromSymbols(secs []binSection, syms []binSymbol, info *binaryInfo) {
	bySect := map[int][]binSymbol{}
	for _, s := range syms {
		if s.sect < 0 || s.sect >= len(secs) {
			continue
		}
		bySect[s.sect] = append(bySect[s.sect], s)
	}

	attrib := map[string]uint64{}
	for si, list := range bySect {
		sec := secs[si]
		if !sec.fileBacked || isDebugSection(sec.name) || sec.isPclntb {
			continue // pclntab is distributed separately; debug/bss excluded
		}
		sort.Slice(list, func(i, j int) bool { return list[i].addr < list[j].addr })
		for i := range list {
			end := sec.size
			if i+1 < len(list) {
				end = list[i+1].addr
			}
			if end < list[i].addr {
				continue
			}
			sz := end - list[i].addr
			pkg := packageOfSymbol(list[i].name)
			attrib[pkg] += sz
			if sec.isText {
				info.CodeSize += sz
				info.CodeSelfSize[pkg] += sz
			} else {
				info.DataSize += sz
			}
		}
	}

	// distribute gopclntab metadata proportionally to each package's code footprint.
	pcln := map[string]uint64{}
	if info.CodeSize > 0 && info.PclntabSize > 0 {
		for pkg, code := range info.CodeSelfSize {
			pcln[pkg] = info.PclntabSize * code / info.CodeSize
		}
	}
	for pkg, v := range attrib {
		info.SelfSize[pkg] = v + pcln[pkg]
	}
}

// attributeStripped recovers code-only sizes from the gopclntab function table.
func attributeStripped(bf binFormat, info *binaryInfo) error {
	pclnData, err := bf.pclntab()
	if err != nil {
		return err
	}
	tab, err := gosym.NewTable(nil, gosym.NewLineTable(pclnData, bf.textStart()))
	if err != nil {
		return fmt.Errorf("parsing gopclntab: %w", err)
	}
	for _, fn := range tab.Funcs {
		if fn.End <= fn.Entry {
			continue
		}
		sz := fn.End - fn.Entry
		pkg := packageOfSymbol(fn.Name)
		info.CodeSize += sz
		info.CodeSelfSize[pkg] += sz
		info.SelfSize[pkg] += sz
	}
	return nil
}

func countAttributable(syms []binSymbol) int {
	n := 0
	for _, s := range syms {
		if s.sect >= 0 {
			n++
		}
	}
	return n
}

func isDebugSection(name string) bool {
	return strings.Contains(name, "debug") || strings.Contains(name, "DWARF")
}

func openBinFormat(path string) (binFormat, error) {
	if f, err := macho.Open(path); err == nil {
		return &machoFormat{f}, nil
	}
	if f, err := elf.Open(path); err == nil {
		return &elfFormat{f}, nil
	}
	if f, err := pe.Open(path); err == nil {
		return newPEFormat(f)
	}
	return nil, fmt.Errorf("%s is not a recognized Mach-O, ELF, or PE binary", path)
}

// --- Mach-O (darwin) ---

type machoFormat struct{ f *macho.File }

func (m *machoFormat) sections() []binSection {
	out := make([]binSection, len(m.f.Sections))
	for i, s := range m.f.Sections {
		out[i] = binSection{
			name:       s.Seg + " " + s.Name,
			addr:       s.Addr,
			size:       s.Size,
			fileBacked: s.Offset != 0, // zero-fill (BSS) sections have no file bytes
			isText:     s.Name == "__text",
			isPclntb:   s.Name == "__gopclntab",
		}
	}
	return out
}
func (m *machoFormat) symbols() []binSymbol {
	if m.f.Symtab == nil {
		return nil
	}
	out := make([]binSymbol, 0, len(m.f.Symtab.Syms))
	for _, s := range m.f.Symtab.Syms {
		if s.Sect == 0 || int(s.Sect) > len(m.f.Sections) {
			continue
		}
		base := m.f.Sections[s.Sect-1].Addr
		if s.Value < base {
			continue
		}
		out = append(out, binSymbol{name: s.Name, addr: s.Value - base, sect: int(s.Sect) - 1})
	}
	return out
}
func (m *machoFormat) textStart() uint64 { return m.f.Section("__text").Addr }
func (m *machoFormat) pclntab() ([]byte, error) {
	s := m.f.Section("__gopclntab")
	if s == nil {
		return nil, fmt.Errorf("no __gopclntab section")
	}
	return s.Data()
}
func (m *machoFormat) close() error { return m.f.Close() }

// --- ELF (linux and friends) ---

type elfFormat struct{ f *elf.File }

func (e *elfFormat) sections() []binSection {
	out := make([]binSection, len(e.f.Sections))
	for i, s := range e.f.Sections {
		out[i] = binSection{
			name:       s.Name,
			addr:       s.Addr,
			size:       s.Size,
			fileBacked: s.Type != elf.SHT_NOBITS,
			isText:     s.Name == ".text",
			isPclntb:   s.Name == ".gopclntab",
		}
	}
	return out
}
func (e *elfFormat) symbols() []binSymbol {
	syms, err := e.f.Symbols()
	if err != nil {
		return nil
	}
	out := make([]binSymbol, 0, len(syms))
	for _, s := range syms {
		idx := int(s.Section)
		if idx < 0 || idx >= len(e.f.Sections) {
			continue
		}
		base := e.f.Sections[idx].Addr
		if s.Value < base {
			continue
		}
		out = append(out, binSymbol{name: s.Name, addr: s.Value - base, sect: idx})
	}
	return out
}
func (e *elfFormat) textStart() uint64 { return e.f.Section(".text").Addr }
func (e *elfFormat) pclntab() ([]byte, error) {
	s := e.f.Section(".gopclntab")
	if s == nil {
		return nil, fmt.Errorf("no .gopclntab section")
	}
	return s.Data()
}
func (e *elfFormat) close() error { return e.f.Close() }

// --- PE (windows) ---
// PE has no named gopclntab section (it lives inside .rdata), so per-section pclntab
// separation isn't possible; pclntab bytes are attributed via .rdata delta-fill and
// PclntabSize is left zero. Symbol values are already section-relative offsets.

type peFormat struct {
	f        *pe.File
	textAddr uint64
	pcln     []byte
}

func newPEFormat(f *pe.File) (*peFormat, error) {
	p := &peFormat{f: f}
	var imageBase uint64
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		imageBase = oh.ImageBase
	case *pe.OptionalHeader32:
		imageBase = uint64(oh.ImageBase)
	default:
		return nil, fmt.Errorf("unsupported PE optional header")
	}
	text := f.Section(".text")
	if text == nil {
		return nil, fmt.Errorf("no .text section")
	}
	p.textAddr = imageBase + uint64(text.VirtualAddress)
	pcln, err := findPEPclntab(f)
	if err != nil {
		return nil, err
	}
	p.pcln = pcln
	return p, nil
}

func (p *peFormat) sections() []binSection {
	out := make([]binSection, len(p.f.Sections))
	for i, s := range p.f.Sections {
		out[i] = binSection{
			name:       s.Name,
			addr:       uint64(s.VirtualAddress),
			size:       uint64(s.VirtualSize),
			fileBacked: s.Size > 0, // SizeOfRawData
			isText:     s.Name == ".text",
			isPclntb:   false,
		}
	}
	return out
}
func (p *peFormat) symbols() []binSymbol {
	out := make([]binSymbol, 0, len(p.f.Symbols))
	for _, s := range p.f.Symbols {
		if s.SectionNumber <= 0 || int(s.SectionNumber) > len(p.f.Sections) {
			continue
		}
		out = append(out, binSymbol{name: s.Name, addr: uint64(s.Value), sect: int(s.SectionNumber) - 1})
	}
	return out
}
func (p *peFormat) textStart() uint64        { return p.textAddr }
func (p *peFormat) pclntab() ([]byte, error) { return p.pcln, nil }
func (p *peFormat) close() error             { return p.f.Close() }

// findPEPclntab locates the gopclntab blob by scanning read-only data sections for the
// runtime pcheader magic (Go 1.16+ variants), then validating header self-consistency.
func findPEPclntab(f *pe.File) ([]byte, error) {
	magics := [][]byte{
		{0xfb, 0xff, 0xff, 0xff}, // go1.2 - 1.15
		{0xfa, 0xff, 0xff, 0xff}, // go1.16 - 1.17
		{0xf0, 0xff, 0xff, 0xff}, // go1.18 - 1.19
		{0xf1, 0xff, 0xff, 0xff}, // go1.20+
	}
	for _, name := range []string{".rdata", ".data", ".text"} {
		s := f.Section(name)
		if s == nil {
			continue
		}
		data, err := s.Data()
		if err != nil {
			continue
		}
		for _, magic := range magics {
			for off := 0; off+8 < len(data); off += 8 {
				if !hasPrefixAt(data, off, magic) {
					continue
				}
				if ptr := data[off+7]; (ptr == 4 || ptr == 8) && data[off+4] == 0 && data[off+5] == 0 {
					return data[off:], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("could not locate gopclntab in PE sections")
}

func hasPrefixAt(b []byte, off int, prefix []byte) bool {
	if off+len(prefix) > len(b) {
		return false
	}
	for i, c := range prefix {
		if b[off+i] != c {
			return false
		}
	}
	return true
}

// packageOfSymbol maps a Go symbol name to its package import path. The package path is
// everything up to the first '.' after the final '/'. Compiler-generated symbols (type:,
// go:, go:string, etc.) and runtime-internal names without a package are bucketed as
// "<generated>". Works for both function and data symbols.
func packageOfSymbol(name string) string {
	if name == "" {
		return "<generated>"
	}
	slash := strings.LastIndexByte(name, '/')
	dot := strings.IndexByte(name[slash+1:], '.')
	if dot < 0 {
		return "<generated>"
	}
	pkg := name[:slash+1+dot]
	if strings.ContainsAny(pkg, ":") || strings.HasPrefix(pkg, "_") {
		return "<generated>"
	}
	return pkg
}
