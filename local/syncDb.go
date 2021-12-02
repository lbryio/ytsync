package local

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
)

type SyncDb struct {
	db *sql.DB
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

func (c *SyncDb) SaveVideoData(video SourceVideo) error {
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

func (c *SyncDb) GetSavedVideoData(source, id string) (*SourceVideo, *string, error) {
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

	var record syncRecord
	err := row.Scan(
		&record.nativeID,
		&record.title,
		&record.description,
		&record.sourceURL,
		&record.releaseTime,
		&record.thumbnailURL,
		&record.fullLocalPath,
		&record.claimID,
	)
	if err == sql.ErrNoRows {
		log.Debugf("Data for YouTube:%s is not in the sync DB", id)
		return nil, nil, nil
	} else if err != nil {
		log.Errorf("Error querying video data for %s:%s from sync DB: %v", source, id, err)
		return nil, nil, err
	}
	sourceVideo, claimID := record.toSourceVideo()

	tags, err := c.getTags(source, id)
	if err != nil {
		return nil, nil, err
	}

	languages, err := c.getLanguages(source, id)
	if err != nil {
		return nil, nil, err
	}

	sourceVideo.Tags = tags
	sourceVideo.Languages = languages

	return &sourceVideo, claimID, nil
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

type syncRecord struct {
	source string
	nativeID string
	title sql.NullString
	description sql.NullString
	sourceURL string
	releaseTime sql.NullInt64
	thumbnailURL sql.NullString
	fullLocalPath string
	claimID sql.NullString
}

func (r *syncRecord) toSourceVideo() (SourceVideo, *string) {
	video := SourceVideo {
		ID: r.nativeID,
		Source: r.source,
		SourceURL: r.sourceURL,
		FullLocalPath: r.fullLocalPath,
	}

	if r.title.Valid {
		video.Title = &r.title.String
	} else {
		video.Title = nil
	}

	if r.description.Valid {
		video.Description = &r.description.String
	} else {
		video.Description = nil
	}

	if r.releaseTime.Valid {
		video.ReleaseTime = &r.releaseTime.Int64
	} else {
		video.ReleaseTime = nil
	}

	if r.thumbnailURL.Valid {
		video.ThumbnailURL = &r.thumbnailURL.String
	} else {
		video.ThumbnailURL = nil
	}

	if r.claimID.Valid {
		return video, &r.claimID.String
	} else {
		return video, nil
	}
}
