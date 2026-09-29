package main

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

const block_size = 16384
const max_backlog = 5

type piece_work struct {
	index  int
	hash   [20]byte
	length int
}

type piece_result struct {
	index int
	data  []byte
}

type piece_progress struct {
	index      int
	buf        []byte
	downloaded int
	requested  int
	backlog    int
}

func (pp *piece_progress) fill_requests(c *client, piece_length int) error {
	for pp.backlog < max_backlog && pp.requested < piece_length {
		block_len := block_size
		if piece_length-pp.requested < block_size {
			block_len = piece_length - pp.requested
		}
		if err := c.send_request(pp.index, pp.requested, block_len); err != nil {
			return err
		}
		pp.backlog++
		pp.requested += block_len
	}
	return nil
}

func (pp *piece_progress) handle_message(c *client) error {
	msg, err := c.read()
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}

	switch msg.id {
	case msg_unchoke:
		c.choked = false

	case msg_choke:
		c.choked = true

	case msg_have:
		index, err := parse_have(msg)
		if err != nil {
			return err
		}
		c.bitfield.set_piece(index)

	case msg_piece:
		n, err := parse_piece(pp.index, pp.buf, msg)
		if err != nil {
			return err
		}
		pp.downloaded += n
		pp.backlog--
	}

	return nil
}

func download_piece(c *client, pw *piece_work) ([]byte, error) {
	pp := &piece_progress{
		index: pw.index,
		buf:   make([]byte, pw.length),
	}

	c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.conn.SetDeadline(time.Time{})

	for pp.downloaded < pw.length {
		if !c.choked {
			if err := pp.fill_requests(c, pw.length); err != nil {
				return nil, err
			}
		}
		if err := pp.handle_message(c); err != nil {
			return nil, err
		}
	}

	return pp.buf, nil
}

func check_integrity(pw *piece_work, data []byte) error {
	hash := sha1.Sum(data)
	if !bytes.Equal(hash[:], pw.hash[:]) {
		return fmt.Errorf("piece %d failed integrity check", pw.index)
	}
	return nil
}

func start_download_worker(p peer, info_hash [20]byte, peer_id [20]byte, work_ch chan *piece_work, results_ch chan *piece_result) {
	c, err := new_client(p, info_hash, peer_id, len(work_ch))
	if err != nil {
		return
	}
	defer c.conn.Close()

	if err := c.send_unchoke(); err != nil {
		return
	}
	if err := c.send_interested(); err != nil {
		return
	}

	for pw := range work_ch {
		if !c.bitfield.has_piece(pw.index) {
			work_ch <- pw
			time.Sleep(30 * time.Millisecond)
			continue
		}

		data, err := download_piece(c, pw)
		if err != nil {
			work_ch <- pw
			return
		}

		if err := check_integrity(pw, data); err != nil {
			work_ch <- pw
			continue
		}

		results_ch <- &piece_result{pw.index, data}
	}
}

func formatSpeed(bytesPerSec float64) string {
	if bytesPerSec >= 1024*1024 {
		return fmt.Sprintf("%.2f MB/s", bytesPerSec/(1024*1024))
	}
	return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02dh %02dm %02ds", h, m, s)
	}
	return fmt.Sprintf("%02dm %02ds", m, s)
}

func renderDashboard(name string, donePieces, totalPieces int, downloadedBytes, totalBytes int64, speed float64, eta time.Duration, workers int) {
	width := 25
	percent := float64(donePieces) / float64(totalPieces)
	completedBlocks := int(percent * float64(width))
	if completedBlocks > width {
		completedBlocks = width
	}

	bar := "["
	for i := 0; i < width; i++ {
		if i < completedBlocks {
			bar += "="
		} else if i == completedBlocks {
			bar += ">"
		} else {
			bar += " "
		}
	}
	bar += "]"

	fmt.Printf("\r\033[K[%s] %s %5.1f%% | %s | %s | ETA: %s | Peers: %d | Pieces: %d/%d",
		name,
		bar,
		percent*100,
		formatSpeed(speed),
		fmt.Sprintf("%.1f/%.1f MB", float64(downloadedBytes)/(1024*1024), float64(totalBytes)/(1024*1024)),
		formatDuration(eta),
		workers,
		donePieces,
		totalPieces,
	)
}

func (t *torrent_file) download(outputPath string) error {
	fmt.Printf("Fetching peers for: %s\n", t.name)

	peer_id, err := new_peer_id()
	if err != nil {
		return err
	}

	peers, err := t.request_peers(peer_id, 6881)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return fmt.Errorf("no peers found from tracker")
	}

	total_pieces := len(t.piece_hashes)
	total_bytes := int64(t.length)
	stateFilePath := outputPath + ".state"

	storage, err := new_storage_manager(outputPath, t.files, total_bytes)
	if err != nil {
		return fmt.Errorf("failed initializing storage layout: %w", err)
	}
	defer storage.Close()

	completedBitfield := make(bitfield, (total_pieces+7)/8)
	done_pieces := 0
	var downloaded_bytes int64

	if stateData, err := os.ReadFile(stateFilePath); err == nil && len(stateData) == len(completedBitfield) {
		copy(completedBitfield, stateData)
		for i := 0; i < total_pieces; i++ {
			if completedBitfield.has_piece(i) {
				pieceLen := t.piece_length_at(i)
				pieceBuf := make([]byte, pieceLen)
				offset := int64(i * t.piece_length)
				if _, err := storage.ReadAt(pieceBuf, offset); err == nil {
					hash := sha1.Sum(pieceBuf)
					if bytes.Equal(hash[:], t.piece_hashes[i][:]) {
						done_pieces++
						downloaded_bytes += int64(pieceLen)
						continue
					}
				}
				byteIdx := i / 8
				bitOffset := i % 8
				completedBitfield[byteIdx] &^= (1 << (7 - bitOffset))
			}
		}
		if done_pieces > 0 {
			fmt.Printf("Resuming download: %d/%d pieces already verified on disk.\n", done_pieces, total_pieces)
		}
	}

	if done_pieces == total_pieces {
		_ = os.Remove(stateFilePath)
		fmt.Println("File is already fully downloaded and verified!")
		return nil
	}

	var stateMu sync.RWMutex
	go startSeeder(6881, *t, peer_id, completedBitfield, storage, &stateMu)

	work_ch := make(chan *piece_work, total_pieces)
	results_ch := make(chan *piece_result)

	for index, hash := range t.piece_hashes {
		if !completedBitfield.has_piece(index) {
			length := t.piece_length_at(index)
			work_ch <- &piece_work{index, hash, length}
		}
	}

	for _, p := range peers {
		go start_download_worker(p, t.info_hash, peer_id, work_ch, results_ch)
	}

	startTime := time.Now()
	lastSampleTime := startTime
	var lastDownloadedBytes int64 = downloaded_bytes
	var currentSpeed float64

	speedTicker := time.NewTicker(500 * time.Millisecond)
	defer speedTicker.Stop()

	for done_pieces < total_pieces {
		select {
		case result := <-results_ch:
			begin := int64(result.index * t.piece_length)
			if err := storage.WriteAt(result.data, begin); err != nil {
				continue
			}

			stateMu.Lock()
			completedBitfield.set_piece(result.index)
			stateMu.Unlock()

			done_pieces++
			downloaded_bytes += int64(len(result.data))
			_ = os.WriteFile(stateFilePath, completedBitfield, 0644)

		case <-speedTicker.C:
			now := time.Now()
			elapsedSec := now.Sub(lastSampleTime).Seconds()
			if elapsedSec > 0 {
				deltaBytes := downloaded_bytes - lastDownloadedBytes
				currentSpeed = float64(deltaBytes) / elapsedSec
				lastDownloadedBytes = downloaded_bytes
				lastSampleTime = now
			}

			var eta time.Duration
			if currentSpeed > 0 {
				remBytes := total_bytes - downloaded_bytes
				eta = time.Duration(float64(remBytes)/currentSpeed) * time.Second
			}

			num_workers := runtime.NumGoroutine() - 1
			renderDashboard(t.name, done_pieces, total_pieces, downloaded_bytes, total_bytes, currentSpeed, eta, num_workers)
		}
	}
	close(work_ch)

	_ = os.Remove(stateFilePath)
	fmt.Printf("\nDownload completed successfully and verified!\n")
	return nil
}

func (t *torrent_file) piece_length_at(index int) int {
	begin := index * t.piece_length
	end := begin + t.piece_length
	if end > t.length {
		end = t.length
	}
	return end - begin
}