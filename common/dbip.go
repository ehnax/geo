package common

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

// LoadDBIP reads the db-ip monthly CSV: start,end,country, no header, so row 1 is data.
func LoadDBIP(path, country string) (out []IPRange, v6, rows int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	var src io.Reader = bufio.NewReaderSize(f, 1<<20)
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(src)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%s: not a gzip file: %w", path, err)
		}
		defer zr.Close()
		src = zr
	}

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		rows++
		line := sc.Text()
		if !strings.Contains(line, country) {
			continue
		}
		// No csv parser: fields hold no commas or quotes, so split is cheaper.
		fields := strings.Split(strings.TrimRight(line, "\r"), ",")
		if len(fields) < 3 {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(fields[2]), country) {
			continue
		}
		loText, hiText := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if strings.Contains(loText, ":") || strings.Contains(hiText, ":") {
			v6++
			continue
		}
		lo, hi := net.ParseIP(loText), net.ParseIP(hiText)
		if lo == nil || hi == nil || lo.To4() == nil || hi.To4() == nil {
			continue
		}
		out = append(out, IPRange{
			Lo: binary.BigEndian.Uint32(lo.To4()),
			Hi: binary.BigEndian.Uint32(hi.To4()),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, v6, rows, fmt.Errorf("%s: parsed, but holds no %s IPv4 ranges", path, country)
	}
	return out, v6, rows, nil
}

// ReadCIDRList reads one of our own lists back; an unparsable line is a writer fault.
func ReadCIDRList(path string) ([]CIDR, error) {
	lines, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	out := make([]CIDR, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c, err := ParseCIDR(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, c)
	}
	return out, nil
}
