package musicbrainz

import "github.com/platten/playlistai/internal/mbindex"

func offlineRecording(row mbindex.Recording) mbRecording {
	length := row.DurationMS
	out := mbRecording{ID: row.MBID, Title: row.Title, Score: 100, FirstReleaseDate: row.FirstReleaseDate, ISRCs: append([]string(nil), row.ISRCs...)}
	if length > 0 {
		out.Length = &length
	}
	for _, credit := range row.Artists {
		value := mbArtistCredit{Name: credit.Name}
		value.Artist.ID = credit.MBID
		value.Artist.Name = credit.Name
		out.ArtistCredit = append(out.ArtistCredit, value)
	}
	for _, tag := range row.Tags {
		out.Tags = append(out.Tags, mbTag{Name: tag.Name, Count: tag.Votes})
	}
	return out
}
