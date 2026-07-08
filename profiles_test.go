package arrapi_test

import (
	"net/http"
	"testing"
)

func TestGetQualityProfiles(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"name":"Anime"},{"id":2,"name":"WEB-HD"}]`)
	s := fastSonarr(t, rs.srv.URL)

	profs, err := s.GetQualityProfiles(t.Context())
	if err != nil {
		t.Fatalf("GetQualityProfiles: %v", err)
	}
	if len(profs) != 2 || profs[0].ID != 1 || profs[0].Name != "Anime" {
		t.Errorf("profiles = %+v, want [{1 Anime} {2 WEB-HD}]", profs)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/qualityprofile?" {
		t.Errorf("path = %q, want /api/v3/qualityprofile?", got)
	}
}

func TestGetRootFolders(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"path":"/media/anime","accessible":true,"freeSpace":1099511627776}]`)
	s := fastSonarr(t, rs.srv.URL)

	folders, err := s.GetRootFolders(t.Context())
	if err != nil {
		t.Fatalf("GetRootFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("got %d root folders, want 1", len(folders))
	}
	rf := folders[0]
	if rf.Path != "/media/anime" || rf.ID != 1 || !rf.Accessible || rf.FreeSpace != 1099511627776 {
		t.Errorf("root folder = %+v", rf)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/rootfolder?" {
		t.Errorf("path = %q, want /api/v3/rootfolder?", got)
	}
}
