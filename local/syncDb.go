package local

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

type SyncDb struct {
	db *sql.DB
}

type SyncDbSummary struct {
	Total int
	CachedUnpublished int
	UncachedUnpublished int
	LatestKnown int64
	LatestPublished int64
}

func NewSyncDb(path string) (*SyncDb, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Errorf("Error opening cache DB at %s: %v", path, err)
		return nil, err
	}

	cache := SyncDb {
		db: db,
	}
	err = cache.ensureSchema()
	if err != nil {
		log.Errorf("Error while ensuring sync DB structure: %v", err)
		return nil, err
	}

	return &cache, nil
}

func (c *SyncDb) Close() error {
	return c.db.Close()
}

func (c *SyncDb) SaveKnownVideo(video SourceVideo) error {
	if video.ID == "" {
		log.Warnf("Trying to save a video with no ID: %v", video)
	}
	insertSql := `
INSERT INTO videos (
	source,
	native_id,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, native_id)
DO NOTHING;
	`

	r := SyncRecordFromSourceVideo(video)

	_, err := c.db.Exec(
		insertSql,
		r.Source,
		r.NativeID,
		r.Title,
		r.Description,
		r.SourceURL,
		r.ReleaseTime,
		r.ThumbnailURL,
		r.FullLocalPath,
	)
	return err
}

func (c *SyncDb) SavePublishableVideo(video PublishableVideo) error {
	upsertSql := `
INSERT INTO videos (
	source,
	native_id,
	claim_name,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, native_id)
DO UPDATE SET
	claim_name = excluded.claim_name,
	title = excluded.title,
	description = excluded.description,
	source_url = excluded.source_url,
	release_time = excluded.release_time,
	thumbnail_url = excluded.thumbnail_url,
	full_local_path = excluded.full_local_path;
	`
	_, err := c.db.Exec(
		upsertSql,
		video.Source,
		video.ID,
		video.ClaimName,
		video.Title,
		video.Description,
		video.SourceURL,
		video.ReleaseTime,
		video.ThumbnailURL,
		video.FullLocalPath,
	)
	if err != nil {
		return err
	}


	err = c.upsertTags(video.Source, video.ID, video.Tags)
	if err != nil {
		return err
	}

	err = c.upsertLanguages(video.Source, video.ID, video.Languages)
	return err
}

func (c *SyncDb) SavePublishedVideo(video PublishedVideo) error {
	upsertSql := `
INSERT INTO videos (
	source,
	native_id,
	claim_id,
	claim_name,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, native_id)
DO UPDATE SET
	claim_id = excluded.claim_id,
	claim_name = excluded.claim_name,
	title = excluded.title,
	description = excluded.description,
	source_url = excluded.source_url,
	release_time = excluded.release_time,
	thumbnail_url = excluded.thumbnail_url,
	full_local_path = excluded.full_local_path;
	`
	_, err := c.db.Exec(
		upsertSql,
		video.Source,
		video.NativeID,
		video.ClaimID,
		video.ClaimName,
		video.Title,
		video.Description,
		video.SourceURL,
		video.ReleaseTime,
		video.ThumbnailURL,
		video.FullLocalPath,
	)
	if err != nil {
		return err
	}


	err = c.upsertTags(video.Source, video.NativeID, video.Tags)
	if err != nil {
		return err
	}

	err = c.upsertLanguages(video.Source, video.NativeID, video.Languages)
	return err
}

func (c *SyncDb) MarkVideoUncached(source, id string) error {
	updateSql := `
UPDATE videos
SET full_local_path = NULL
WHERE source = ? AND native_id = ?;
	`
	_, err := c.db.Exec(
		updateSql,
		source,
		id,
	)
	return err
}

func (c *SyncDb) _SaveVideoData(video SourceVideo) error {
	upsertSql := `
INSERT INTO videos (
	source,
	native_id,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, native_id)
DO UPDATE SET
	title = excluded.title,
	description = excluded.description,
	source_url = excluded.source_url,
	release_time = excluded.release_time,
	thumbnail_url = excluded.thumbnail_url,
	full_local_path = excluded.full_local_path;
	`
	_, err := c.db.Exec(
		upsertSql,
		video.Source,
		video.ID,
		video.Title,
		video.Description,
		video.SourceURL,
		video.ReleaseTime,
		video.ThumbnailURL,
		video.FullLocalPath,
	)
	if err != nil {
		return err
	}

	err = c.upsertTags(video.Source, video.ID, video.Tags)
	if err != nil {
		return err
	}

	err = c.upsertLanguages(video.Source, video.ID, video.Languages)
	return err
}

func (c *SyncDb) SaveVideoPublication(video PublishableVideo, claimID string) error {
	upsertSql := `
INSERT INTO videos (
	source,
	native_id,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path,
	claim_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, native_id)
DO UPDATE SET
	title = excluded.title,
	description = excluded.description,
	source_url = excluded.source_url,
	release_time = excluded.release_time,
	thumbnail_url = excluded.thumbnail_url,
	full_local_path = excluded.full_local_path,
	claim_id = excluded.claim_id;
	`
	_, err := c.db.Exec(
		upsertSql,
		video.Source,
		video.ID,
		video.Title,
		video.Description,
		video.SourceURL,
		video.ReleaseTime,
		video.ThumbnailURL,
		video.FullLocalPath,
		claimID,
	)
	if err != nil {
		return err
	}

	err = c.upsertTags(video.Source, video.ID, video.Tags)
	if err != nil {
		return err
	}

	err = c.upsertLanguages(video.Source, video.ID, video.Languages)
	return err
}

func (c *SyncDb) IsVideoPublished(source, id string) (bool, string, error) {
	selectSql := `
SELECT
	claim_id
FROM videos
WHERE source = ? AND native_id = ?
	`
	row := c.db.QueryRow(selectSql, source, id)

	var claimID sql.NullString
	err := row.Scan(&claimID)

	if err == sql.ErrNoRows {
		return false, "", nil
	} else if err != nil {
		log.Errorf("Error querying video publication for %s:%s from sync DB: %v", source, id, err)
		return false, "", err
	}

	if claimID.Valid {
		return true, claimID.String, nil
	} else {
		return false, "", nil
	}
}

func (c *SyncDb) IsVideoCached(source, id string) (bool, string, error) {
	selectSql := `
SELECT
	full_local_path
FROM videos
WHERE source = ? AND native_id = ?
	`
	row := c.db.QueryRow(selectSql, source, id)

	var localPath sql.NullString
	err := row.Scan(&localPath)

	if err == sql.ErrNoRows {
		return false, "", nil
	} else if err != nil {
		log.Errorf("Error querying video cache status for %s:%s from sync DB: %v", source, id, err)
		return false, "", err
	}

	if localPath.Valid {
		return true, localPath.String, nil
	} else {
		return false, "", nil
	}
}

func (c *SyncDb) GetVideoRecord(source, id string, includeTags, includeLanguages bool) (*SyncRecord, error) {
	selectSql := `
SELECT
	native_id,
	title,
	description,
	source_url,
	release_time,
	thumbnail_url,
	full_local_path,
	claim_id
FROM videos
WHERE source = ? AND native_id = ?
	`
	row := c.db.QueryRow(selectSql, source, id)

	var record SyncRecord
	err := row.Scan(
		&record.NativeID,
		&record.Title,
		&record.Description,
		&record.SourceURL,
		&record.ReleaseTime,
		&record.ThumbnailURL,
		&record.FullLocalPath,
		&record.ClaimID,
	)
	if err == sql.ErrNoRows {
		log.Debugf("Data for %s:%s is not in the sync DB", source, id)
		return nil, nil
	} else if err != nil {
		log.Errorf("Error querying video data for %s:%s from sync DB: %v", source, id, err)
		return nil, err
	}

	if includeTags {
		tags, err := c.getTags(source, id)
		if err != nil {
			return nil, err
		}
		record.Tags = &tags
	}

	if includeLanguages {
		languages, err := c.getLanguages(source, id)
		if err != nil {
			return nil, err
		}
		record.Languages = &languages
	}

	return &record, nil
}

func (c *SyncDb) GetUnpublishedIDs(source string) ([]string, error) {
	selectSql := `
SELECT
	native_id
FROM videos
WHERE source = ? AND claim_id IS NULL
	`
	ids := []string{}

	rows, err := c.db.Query(selectSql, source)
	if err != nil {
		return ids, err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		err = rows.Scan(&id)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

func (c *SyncDb) GetSummary() (*SyncDbSummary, error) {
	selectSql := `
SELECT
	COUNT() AS total,
	COUNT(v_unpub.full_local_path) AS cached_unpublished,
	COUNT(v_all.claim_id) - COUNT(v_unpub.full_local_path) AS uncached_unpublished,
	MAX(v_all.release_time) AS latest_known,
	MAX(v_pub.release_time) AS latest_published
FROM videos v_all
LEFT JOIN videos v_pub ON v_all.source = v_pub.source AND v_all.native_id = v_pub.native_id AND v_pub.claim_id IS NOT NULL
LEFT JOIN videos v_unpub ON v_all.source = v_unpub.source AND v_all.native_id = v_unpub.native_id AND v_unpub.claim_id IS NULL
	`
	row := c.db.QueryRow(selectSql)

	var summary SyncDbSummary
	var latestKnown, latestPublished sql.NullInt64
	err := row.Scan(
		&summary.Total,
		&summary.CachedUnpublished,
		&summary.UncachedUnpublished,
		&latestKnown,
		&latestPublished,
	)
	if err != nil {
		log.Errorf("Error querying sync DB summary: %v", err)
		return nil, err
	}

	if latestKnown.Valid {
		summary.LatestKnown = latestKnown.Int64
	}
	if latestPublished.Valid {
		summary.LatestPublished = latestPublished.Int64
	}

	return &summary, nil
}

func (c *SyncDb) ensureSchema() error {
	createSql := `
CREATE TABLE IF NOT EXISTS videos (
	source TEXT,
	native_id TEXT,
	title TEXT,
	description TEXT,
	source_url TEXT,
	release_time INT,
	thumbnail_url TEXT,
	full_local_path TEXT,
	claim_name TEXT,
	claim_id TEXT,
	PRIMARY KEY (source, native_id)
);
CREATE TABLE IF NOT EXISTS video_tags (
	source TEXT NOT NULL,
	native_id TEXT NOT NULL,
	tag TEXT NOT NULL,
	UNIQUE (source, native_id, tag)
);
CREATE TABLE IF NOT EXISTS video_languages (
	source TEXT NOT NULL,
	native_id TEXT NOT NULL,
	language TEXT NOT NULL,
	UNIQUE (source, native_id, language)
);
	`
	_, err := c.db.Exec(createSql)
	return err
}

func (c *SyncDb) upsertTags(source, id string, tags []string) error {
	upsertSql := `
INSERT INTO video_tags (
	source,
	native_id,
	tag
) VALUES (?, ?, ?)
ON CONFLICT (source, native_id, tag)
DO NOTHING;
	`

	for _, tag := range tags {
		_, err := c.db.Exec(
			upsertSql,
			source,
			id,
			tag,
		)
		if err != nil {
			log.Errorf("Error inserting tag %s into sync DB for %s:%s: %v", tag, source, id, err)
			return err
		}
	}
	return nil
}

func (c *SyncDb) getTags(source, id string) ([]string, error) {
	selectSql := `
SELECT tag
FROM video_tags
WHERE source = ? AND native_id = ?;
	`

	rows, err := c.db.Query(selectSql, source, id)
	if err != nil {
		log.Errorf("Error getting tags from sync DB for %s:%s: %v", source, id, err)
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		err = rows.Scan(&tag)
		if err != nil {
			log.Error("Error deserializing tag from sync DB for %s:%s: %v", source, id, err)
			return nil, err
		}
		tags = append(tags, tag)
	}

	return tags, nil
}

func (c *SyncDb) upsertLanguages(source, id string, languages []string) error {
	upsertSql := `
INSERT INTO video_languages (
	source,
	native_id,
	language
) VALUES (?, ?, ?)
ON CONFLICT (source, native_id, language)
DO NOTHING;
	`

	for _, language := range languages {
		_, err := c.db.Exec(
			upsertSql,
			source,
			id,
			language,
		)
		if err != nil {
			log.Errorf("Error inserting language %s into sync DB for %s:%s: %v", language, source, id, err)
			return err
		}
	}
	return nil
}

func (c *SyncDb) getLanguages(source, id string) ([]string, error) {
	selectSql := `
SELECT language
FROM video_languages
WHERE source = ? AND native_id = ?;
	`

	rows, err := c.db.Query(selectSql, source, id)
	if err != nil {
		log.Errorf("Error getting languages from sync DB for %s:%s: %v", source, id, err)
		return nil, err
	}
	defer rows.Close()

	var languages []string
	for rows.Next() {
		var language string
		err = rows.Scan(&language)
		if err != nil {
			log.Error("Error deserializing language from sync DB for %s:%s: %v", source, id, err)
			return nil, err
		}
		languages = append(languages, language)
	}

	return languages, nil
}

type SyncRecord struct {
	Source string
	NativeID string
	Title sql.NullString
	Description sql.NullString
	SourceURL sql.NullString
	ReleaseTime sql.NullInt64
	ThumbnailURL sql.NullString
	FullLocalPath sql.NullString
	ClaimID sql.NullString
	Tags *[]string
	Languages *[]string
}

func SyncRecordFromSourceVideo(v SourceVideo) SyncRecord {
	r := SyncRecord {
		Source: v.Source,
		NativeID: v.ID,
		SourceURL: sql.NullString { String: v.SourceURL, Valid: true },
	}

	if v.Title != nil {
		r.Title = sql.NullString { String: *v.Title, Valid: true }
	}

	if v.Description != nil {
		r.Description = sql.NullString { String: *v.Description, Valid: true }
	}

	if v.ThumbnailURL != nil {
		r.ThumbnailURL = sql.NullString { String: *v.ThumbnailURL, Valid: true }
	}

	if v.FullLocalPath != nil {
		r.FullLocalPath = sql.NullString { String: *v.FullLocalPath, Valid: true }
	}

	if v.ReleaseTime != nil {
		r.ReleaseTime = sql.NullInt64 { Int64: *v.ReleaseTime, Valid: true }
	}

	if len(v.Tags) > 0 {
		r.Tags = &v.Tags
	}

	if len(v.Languages) > 0 {
		r.Languages = &v.Languages
	}

	return r
}

func (r *SyncRecord) ToPublishableVideo() *PublishableVideo {
	if !(r.Title.Valid &&
		r.Description.Valid &&
		r.SourceURL.Valid &&
		r.ReleaseTime.Valid &&
		r.ThumbnailURL.Valid &&
		r.FullLocalPath.Valid &&
		r.Tags != nil &&
		r.Languages != nil) {

		return nil
	}

	video := PublishableVideo {
		ID: r.NativeID,
		Source: r.Source,
		Description: r.Description.String,
		SourceURL: r.SourceURL.String,
		ReleaseTime: r.ReleaseTime.Int64,
		ThumbnailURL: r.ThumbnailURL.String,
		FullLocalPath: r.FullLocalPath.String,
		Tags: *r.Tags,
		Languages: *r.Languages,
	}

	return &video
}
