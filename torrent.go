package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackpal/bencode-go"
)

type bencode_file struct {
	Length int      `bencode:"length"`
	Path   []string `bencode:"path"`
}

type bencode_info struct {
	Pieces      string         `bencode:"pieces"`
	PieceLength int            `bencode:"piece length"`
	Length      int            `bencode:"length"` // single-file mode
	Name        string         `bencode:"name"`
	Files       []bencode_file `bencode:"files"` // multi-file mode
}

type bencode_torrent struct {
	Announce string       `bencode:"announce"`
	Info     bencode_info `bencode:"info"`
}

type file_slice struct {
	path   string
	length int
	offset int64
}

type torrent_file struct {
	announce     string
	info_hash    [20]byte
	piece_hashes [][20]byte
	piece_length int
	length       int
	name         string
	files        []file_slice
}

func extractInfoBytes(data []byte) ([]byte, error) {
	key := []byte("4:info")
	idx := bytes.Index(data, key)
	if idx == -1 {
		return nil, fmt.Errorf("missing 4:info in torrent")
	}

	start := idx + len(key)
	if start >= len(data) || data[start] != 'd' {
		return nil, fmt.Errorf("info value is not a bencoded dictionary")
	}

	depth := 0
	i := start
	for i < len(data) {
		switch data[i] {
		case 'd', 'l':
			depth++
			i++
		case 'e':
			depth--
			i++
			if depth == 0 {
				return data[start:i], nil
			}
		case 'i':
			i++
			for i < len(data) && data[i] != 'e' {
				i++
			}
			if i < len(data) {
				i++ // skip 'e'
			}
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			colonIdx := bytes.IndexByte(data[i:], ':')
			if colonIdx == -1 {
				return nil, fmt.Errorf("malformed bencode string length")
			}
			colonIdx += i
			strLen, err := strconv.Atoi(string(data[i:colonIdx]))
			if err != nil {
				return nil, err
			}
			i = colonIdx + 1 + strLen
		default:
			i++
		}
	}

	return nil, fmt.Errorf("unmatched bencode end tag")
}

func open(path string) (torrent_file, error) {
	fileData, err := os.ReadFile(path)
	if err != nil {
		return torrent_file{}, err
	}

	raw := &bencode_torrent{}
	if err := bencode.Unmarshal(bytes.NewReader(fileData), raw); err != nil {
		return torrent_file{}, err
	}

	rawInfoBytes, err := extractInfoBytes(fileData)
	if err != nil {
		return torrent_file{}, err
	}
	infoHash := sha1.Sum(rawInfoBytes)

	pieceHashes, err := raw.Info.split_piece_hashes()
	if err != nil {
		return torrent_file{}, err
	}

	totalLength := raw.Info.Length
	var files []file_slice

	if len(raw.Info.Files) > 0 {
		totalLength = 0
		var currentOffset int64 = 0
		for _, f := range raw.Info.Files {
			relPath := filepath.Join(append([]string{raw.Info.Name}, f.Path...)...)
			files = append(files, file_slice{
				path:   relPath,
				length: f.Length,
				offset: currentOffset,
			})
			currentOffset += int64(f.Length)
			totalLength += f.Length
		}
	} else {
		files = append(files, file_slice{
			path:   raw.Info.Name,
			length: raw.Info.Length,
			offset: 0,
		})
	}

	return torrent_file{
		announce:     raw.Announce,
		info_hash:    infoHash,
		piece_hashes: pieceHashes,
		piece_length: raw.Info.PieceLength,
		length:       totalLength,
		name:         raw.Info.Name,
		files:        files,
	}, nil
}

func (i *bencode_info) split_piece_hashes() ([][20]byte, error) {
	buf := []byte(i.Pieces)
	if len(buf)%20 != 0 {
		return nil, fmt.Errorf("malformed pieces: length %d not a multiple of 20", len(buf))
	}

	n := len(buf) / 20
	hashes := make([][20]byte, n)
	for j := 0; j < n; j++ {
		copy(hashes[j][:], buf[j*20:(j+1)*20])
	}
	return hashes, nil
}

type peer struct {
	ip   [4]byte
	port uint16
}

func unmarshal_peers(data []byte) ([]peer, error) {
	if len(data)%6 != 0 {
		return nil, fmt.Errorf("malformed peers: length %d not a multiple of 6", len(data))
	}

	n := len(data) / 6
	peers := make([]peer, n)

	for i := 0; i < n; i++ {
		copy(peers[i].ip[:], data[i*6:i*6+4])
		peers[i].port = binary.BigEndian.Uint16(data[i*6+4 : i*6+6])
	}
	return peers, nil
}

func (p peer) String() string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", p.ip[0], p.ip[1], p.ip[2], p.ip[3], p.port)
}

func (t *torrent_file) build_tracker_url(peer_id [20]byte, port uint16) (string, error) {
	base, err := url.Parse(t.announce)
	if err != nil {
		return "", err
	}

	params := url.Values{
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(t.length)},
		"numwant":    []string{"100"},
	}

	escapeBytes := func(b []byte) string {
		var buf bytes.Buffer
		for _, c := range b {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
				buf.WriteByte(c)
			} else {
				fmt.Fprintf(&buf, "%%%02X", c)
			}
		}
		return buf.String()
	}

	rawQuery := fmt.Sprintf("info_hash=%s&peer_id=%s&%s",
		escapeBytes(t.info_hash[:]),
		escapeBytes(peer_id[:]),
		params.Encode(),
	)

	base.RawQuery = rawQuery
	return base.String(), nil
}

func (t *torrent_file) request_peers(peer_id [20]byte, port uint16) ([]peer, error) {
	u, err := url.Parse(t.announce)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "udp" {
		return request_peers_udp(u, t.info_hash, peer_id, port, t.length)
	}

	trackerURL, err := t.build_tracker_url(peer_id, port)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	response, err := httpClient.Get(trackerURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var trackerResp struct {
		Peers string `bencode:"peers"`
	}

	if err := bencode.Unmarshal(bytes.NewReader(body), &trackerResp); err != nil {
		return nil, err
	}

	return unmarshal_peers([]byte(trackerResp.Peers))
}

func new_peer_id() ([20]byte, error) {
	var id [20]byte
	_, err := rand.Read(id[:])
	return id, err
}
