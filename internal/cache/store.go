package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const CacheSchemaVersion = 1

var (
	ErrSectionCorrupt = errors.New("snapshot section integrity check failed")
)

type SectionMeta struct {
	Section       string          `json:"section"`
	SnapshotID    string          `json:"snapshotId"`
	CompanyID     string          `json:"companyId"`
	GeneratedAt   string          `json:"generatedAt"`
	ContentSHA256 string          `json:"contentSha256"`
	RecordCount   int             `json:"recordCount"`
	SummariesJSON json.RawMessage `json:"summaries,omitempty"`
}

type ChunkPayload struct {
	Sequence    int    `json:"sequence"`
	SHA256      string `json:"sha256"`
	RecordCount int    `json:"recordCount"`
	RawJSON     []byte `json:"rawJson"`
}

type ChunkDocument struct {
	ProtocolVersion    int               `json:"protocolVersion"`
	CacheSchemaVersion int               `json:"cacheSchemaVersion"`
	CompanyID          string            `json:"companyId"`
	Section            string            `json:"section"`
	SnapshotID         string            `json:"snapshotId"`
	Sequence           int               `json:"sequence"`
	RecordCount        int               `json:"recordCount"`
	SHA256             string            `json:"sha256"`
	Records            []ChunkRecordItem `json:"records"`
	Summaries          json.RawMessage   `json:"summaries"`
}

type ChunkRecordItem struct {
	EntityID string          `json:"entityId"`
	SortKey  string          `json:"sortKey"`
	DTO      json.RawMessage `json:"dto"`
}

type Record struct {
	Section  string          `json:"section"`
	EntityID string          `json:"entityId"`
	SortKey  string          `json:"sortKey"`
	DTO      json.RawMessage `json:"dto"`
}

type Store struct {
	db *sql.DB
}

func NewStore(ctx context.Context, sqlDB *sql.DB) (*Store, error) {
	if sqlDB == nil {
		return nil, errors.New("sql.DB is required")
	}
	s := &Store{db: sqlDB}
	if err := s.initSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS cache_sections (
	section TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL,
	company_id TEXT NOT NULL,
	generated_at TEXT NOT NULL,
	content_sha256 TEXT NOT NULL,
	record_count INTEGER NOT NULL DEFAULT 0,
	summaries_json TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cache_records (
	section TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	sort_key TEXT NOT NULL,
	dto_json TEXT NOT NULL,
	PRIMARY KEY (section, entity_id)
);

CREATE INDEX IF NOT EXISTS idx_cache_records_section_sort ON cache_records(section, sort_key);
`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

func canonicalUnsignedChunk(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}

	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("chunk must be a JSON object")
	}
	delete(object, "sha256")

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

func (s *Store) ApplySection(ctx context.Context, meta SectionMeta, chunks []ChunkPayload) error {
	meta.Section = strings.TrimSpace(meta.Section)
	meta.SnapshotID = strings.TrimSpace(meta.SnapshotID)
	meta.CompanyID = strings.TrimSpace(meta.CompanyID)
	meta.ContentSHA256 = strings.ToLower(strings.TrimSpace(meta.ContentSHA256))

	if meta.Section == "" || meta.SnapshotID == "" || meta.CompanyID == "" {
		return fmt.Errorf("%w: missing section metadata", ErrSectionCorrupt)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("%w: complete section has no chunks", ErrSectionCorrupt)
	}

	sorted := append([]ChunkPayload(nil), chunks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })

	var parsedRecords []ChunkRecordItem
	summariesRaw := "[]"
	totalRecords := 0
	sectionHash := sha256.New()
	sectionHash.Write([]byte("["))

	for idx, ch := range sorted {
		if ch.Sequence != idx {
			return fmt.Errorf("%w: non-contiguous chunk sequence at %d (got %d)", ErrSectionCorrupt, idx, ch.Sequence)
		}

		unsigned, err := canonicalUnsignedChunk(ch.RawJSON)
		if err != nil {
			return fmt.Errorf("%w: chunk %d invalid JSON: %v", ErrSectionCorrupt, ch.Sequence, err)
		}
		sum := sha256.Sum256(unsigned)
		gotHex := hex.EncodeToString(sum[:])
		wantHex := strings.ToLower(strings.TrimSpace(ch.SHA256))
		if wantHex == "" || gotHex != wantHex {
			return fmt.Errorf("%w: chunk %d sha256 mismatch (got %s, want %s)", ErrSectionCorrupt, ch.Sequence, gotHex, wantHex)
		}

		var doc ChunkDocument
		if err := json.Unmarshal(ch.RawJSON, &doc); err != nil {
			return fmt.Errorf("%w: chunk %d invalid JSON: %v", ErrSectionCorrupt, ch.Sequence, err)
		}
		if doc.ProtocolVersion != 1 {
			return fmt.Errorf("%w: chunk %d protocolVersion %d != 1", ErrSectionCorrupt, ch.Sequence, doc.ProtocolVersion)
		}
		if doc.CacheSchemaVersion != CacheSchemaVersion {
			return fmt.Errorf("%w: chunk %d cacheSchemaVersion %d != %d", ErrSectionCorrupt, ch.Sequence, doc.CacheSchemaVersion, CacheSchemaVersion)
		}
		if doc.CompanyID != meta.CompanyID {
			return fmt.Errorf("%w: chunk %d companyId %q != %q", ErrSectionCorrupt, ch.Sequence, doc.CompanyID, meta.CompanyID)
		}
		if doc.Section != meta.Section {
			return fmt.Errorf("%w: chunk %d section %q != %q", ErrSectionCorrupt, ch.Sequence, doc.Section, meta.Section)
		}
		if doc.SnapshotID != meta.SnapshotID {
			return fmt.Errorf("%w: chunk %d snapshotId %q != %q", ErrSectionCorrupt, ch.Sequence, doc.SnapshotID, meta.SnapshotID)
		}
		if doc.Sequence != ch.Sequence {
			return fmt.Errorf("%w: chunk %d document sequence %d != descriptor sequence", ErrSectionCorrupt, ch.Sequence, doc.Sequence)
		}
		if doc.RecordCount != len(doc.Records) {
			return fmt.Errorf("%w: chunk %d document record count %d != %d", ErrSectionCorrupt, ch.Sequence, doc.RecordCount, len(doc.Records))
		}
		if ch.RecordCount != len(doc.Records) {
			return fmt.Errorf("%w: chunk %d descriptor record count %d != %d", ErrSectionCorrupt, ch.Sequence, ch.RecordCount, len(doc.Records))
		}
		wireSHA256 := strings.ToLower(strings.TrimSpace(doc.SHA256))
		if wireSHA256 == "" || wireSHA256 != gotHex {
			return fmt.Errorf("%w: chunk %d wire sha256 mismatch", ErrSectionCorrupt, ch.Sequence)
		}
		for _, rec := range doc.Records {
			if strings.TrimSpace(rec.EntityID) == "" || len(rec.DTO) == 0 {
				return fmt.Errorf("%w: chunk %d record missing entityId or dto", ErrSectionCorrupt, ch.Sequence)
			}
			parsedRecords = append(parsedRecords, rec)
		}
		if idx == 0 && len(doc.Summaries) > 0 {
			summariesRaw = string(doc.Summaries)
		}
		totalRecords += len(doc.Records)
		if idx > 0 {
			sectionHash.Write([]byte(","))
		}
		sectionHash.Write(unsigned)
	}
	sectionHash.Write([]byte("]"))

	if totalRecords != meta.RecordCount {
		return fmt.Errorf("%w: total record count %d != expected %d", ErrSectionCorrupt, totalRecords, meta.RecordCount)
	}
	if gotHex := hex.EncodeToString(sectionHash.Sum(nil)); meta.ContentSHA256 != gotHex {
		return fmt.Errorf("%w: section contentSha256 mismatch (got %s, want %s)", ErrSectionCorrupt, gotHex, meta.ContentSHA256)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM cache_records WHERE section = ?`, meta.Section); err != nil {
		return err
	}
	for _, rec := range parsedRecords {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cache_records (section, entity_id, sort_key, dto_json)
			VALUES (?, ?, ?, ?)
		`, meta.Section, rec.EntityID, rec.SortKey, string(rec.DTO)); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO cache_sections (
			section, snapshot_id, company_id, generated_at, content_sha256, record_count, summaries_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(section) DO UPDATE SET
			snapshot_id = excluded.snapshot_id,
			company_id = excluded.company_id,
			generated_at = excluded.generated_at,
			content_sha256 = excluded.content_sha256,
			record_count = excluded.record_count,
			summaries_json = excluded.summaries_json,
			updated_at = excluded.updated_at
	`, meta.Section, meta.SnapshotID, meta.CompanyID, meta.GeneratedAt, meta.ContentSHA256, meta.RecordCount, summariesRaw, now)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) ListRecords(ctx context.Context, section string, limit, offset int) ([]Record, SectionMeta, error) {
	section = strings.TrimSpace(section)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var meta SectionMeta
	var summariesStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT section, snapshot_id, company_id, generated_at, content_sha256, record_count, summaries_json
		FROM cache_sections
		WHERE section = ?
	`, section).Scan(
		&meta.Section,
		&meta.SnapshotID,
		&meta.CompanyID,
		&meta.GeneratedAt,
		&meta.ContentSHA256,
		&meta.RecordCount,
		&summariesStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, SectionMeta{Section: section}, nil
	}
	if err != nil {
		return nil, SectionMeta{}, err
	}
	meta.SummariesJSON = json.RawMessage(summariesStr)

	rows, err := s.db.QueryContext(ctx, `
		SELECT section, entity_id, sort_key, dto_json
		FROM cache_records
		WHERE section = ?
		ORDER BY sort_key ASC, entity_id ASC
		LIMIT ? OFFSET ?
	`, section, limit, offset)
	if err != nil {
		return nil, meta, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var dtoStr string
		if err := rows.Scan(&r.Section, &r.EntityID, &r.SortKey, &dtoStr); err != nil {
			return nil, meta, err
		}
		r.DTO = json.RawMessage(dtoStr)
		out = append(out, r)
	}
	return out, meta, rows.Err()
}

func (s *Store) GetSectionMeta(ctx context.Context, section string) (SectionMeta, bool, error) {
	var meta SectionMeta
	var summariesStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT section, snapshot_id, company_id, generated_at, content_sha256, record_count, summaries_json
		FROM cache_sections
		WHERE section = ?
	`, strings.TrimSpace(section)).Scan(
		&meta.Section,
		&meta.SnapshotID,
		&meta.CompanyID,
		&meta.GeneratedAt,
		&meta.ContentSHA256,
		&meta.RecordCount,
		&summariesStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SectionMeta{}, false, nil
	}
	if err != nil {
		return SectionMeta{}, false, err
	}
	meta.SummariesJSON = json.RawMessage(summariesStr)
	return meta, true, nil
}

func (s *Store) WipeCompanyCache(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM cache_records`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cache_sections`); err != nil {
		return err
	}
	return tx.Commit()
}
