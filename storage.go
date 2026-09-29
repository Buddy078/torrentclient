package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type storage_manager struct {
	baseDir string
	slices  []file_slice
	handles map[string]*os.File
	mu      sync.RWMutex
}

func new_storage_manager(baseDir string, slices []file_slice, totalBytes int64) (*storage_manager, error) {
	sm := &storage_manager{
		baseDir: baseDir,
		slices:  slices,
		handles: make(map[string]*os.File),
	}

	if len(slices) == 1 {
		// Single-file direct output
		if err := os.MkdirAll(filepath.Dir(baseDir), 0755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(baseDir, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed opening file %s: %w", baseDir, err)
		}
		if stat, err := f.Stat(); err == nil && stat.Size() < totalBytes {
			_ = f.Truncate(totalBytes)
		}
		sm.handles[slices[0].path] = f
		return sm, nil
	}

	// Multi-file directory hierarchy
	for _, s := range slices {
		fullPath := filepath.Join(baseDir, s.path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, err
		}

		f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed opening slice %s: %w", fullPath, err)
		}

		if stat, err := f.Stat(); err == nil && stat.Size() < int64(s.length) {
			_ = f.Truncate(int64(s.length))
		}

		sm.handles[s.path] = f
	}

	return sm, nil
}

func (sm *storage_manager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, f := range sm.handles {
		_ = f.Close()
	}
}

func (sm *storage_manager) WriteAt(data []byte, offset int64) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	dataOffset := 0
	dataRemaining := len(data)

	for _, s := range sm.slices {
		sliceStart := s.offset
		sliceEnd := s.offset + int64(s.length)

		currGlobalPos := offset + int64(dataOffset)
		if currGlobalPos >= sliceStart && currGlobalPos < sliceEnd {
			sliceLocalOffset := currGlobalPos - sliceStart
			availableInSlice := int(sliceEnd - currGlobalPos)

			writeLen := dataRemaining
			if writeLen > availableInSlice {
				writeLen = availableInSlice
			}

			f := sm.handles[s.path]
			if _, err := f.WriteAt(data[dataOffset:dataOffset+writeLen], sliceLocalOffset); err != nil {
				return err
			}

			dataOffset += writeLen
			dataRemaining -= writeLen
			if dataRemaining == 0 {
				break
			}
		}
	}
	return nil
}

func (sm *storage_manager) ReadAt(buf []byte, offset int64) (int, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	dataOffset := 0
	dataRemaining := len(buf)

	for _, s := range sm.slices {
		sliceStart := s.offset
		sliceEnd := s.offset + int64(s.length)

		currGlobalPos := offset + int64(dataOffset)
		if currGlobalPos >= sliceStart && currGlobalPos < sliceEnd {
			sliceLocalOffset := currGlobalPos - sliceStart
			availableInSlice := int(sliceEnd - currGlobalPos)

			readLen := dataRemaining
			if readLen > availableInSlice {
				readLen = availableInSlice
			}

			f := sm.handles[s.path]
			n, err := f.ReadAt(buf[dataOffset:dataOffset+readLen], sliceLocalOffset)
			dataOffset += n
			dataRemaining -= n
			if err != nil && err != io.EOF {
				return dataOffset, err
			}
			if dataRemaining == 0 {
				break
			}
		}
	}
	return dataOffset, nil
}