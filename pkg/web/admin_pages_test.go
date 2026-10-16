package web

import (
	"net/http"
	"testing"

	"keywordhunter-mvp/pkg/storage"
)

// TestAdminPages_OnlyAdmin: /users ve /settings sayfaları yalnız admin içindir.
// Menüde gizlenmeleri yetmez; URL ile doğrudan erişim de sunucuda reddedilmeli.
func TestAdminPages_OnlyAdmin(t *testing.T) {
	s := newTestServer(t)
	adminCk := loginAs(t, s, "admin", "correct-horse")
	viewerCk := mkUser(t, s, "pv1", "viewer-pass-1", storage.RoleViewer)
	analystCk := mkUser(t, s, "pa1", "analyst-pass-1", storage.RoleAnalyst)

	for _, path := range []string{"/users", "/settings"} {
		if w := do(s, http.MethodGet, path, nil, adminCk, nil); w.Code != http.StatusOK {
			t.Fatalf("admin %s: %d (200 bekleniyordu)", path, w.Code)
		}
		for name, ck := range map[string][]*http.Cookie{"viewer": viewerCk, "analyst": analystCk} {
			if w := do(s, http.MethodGet, path, nil, ck, nil); w.Code != http.StatusForbidden {
				t.Fatalf("%s %s: %d (403 bekleniyordu)", name, path, w.Code)
			}
		}
	}
	// Planlı taramalar ve izleme listesi her role açık (yazma kontrolleri rolle gizlenir)
	for _, path := range []string{"/scheduled", "/watchlist"} {
		if w := do(s, http.MethodGet, path, nil, viewerCk, nil); w.Code != http.StatusOK {
			t.Fatalf("viewer %s: %d", path, w.Code)
		}
	}
}
