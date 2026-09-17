package localaudio

import "testing"

func TestParseMetadataPreservesRawNamesAndDoesNotSplit(t *testing.T) {
	metadata, err := parseMetadata(map[string]string{
		"TITLE": "Track", "artist": "AC/DC & Guest", "album_artist": "Various Artists",
		"genre": "R&B/Pop", "track": "2/9", "discnumber": "1/2", "ISRC": "USTST2600001",
		"MUSICBRAINZ_TRACKID": "mbid", "REPLAYGAIN_TRACK_GAIN": "-4.0 dB",
	}, map[string]string{"artist": "stream should not replace format"}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.ArtistCredits) != 1 || metadata.ArtistCredits[0].Value != "AC/DC & Guest" ||
		len(metadata.Genres) != 1 || metadata.Genres[0].Value != "R&B/Pop" {
		t.Fatalf("artist or genre was split/changed: %+v", metadata)
	}
	if metadata.Track.Number != 2 || metadata.Track.Total != 9 || metadata.Disc.Number != 1 || metadata.Disc.Total != 2 {
		t.Fatalf("track/disc parsing failed: %+v %+v", metadata.Track, metadata.Disc)
	}
	if metadata.MusicBrainzIDs["MUSICBRAINZ_TRACKID"] != "mbid" || metadata.ReplayGain["REPLAYGAIN_TRACK_GAIN"] != "-4.0 dB" {
		t.Fatalf("provenance tags lost: %+v", metadata)
	}
}

func TestParseMetadataEnforcesLimit(t *testing.T) {
	if _, err := parseMetadata(map[string]string{"title": "too long"}, nil, 2); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}
