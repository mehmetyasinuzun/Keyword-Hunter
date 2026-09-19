package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidTarget_RespectsPrivateSwitch(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "")
	if ValidTarget("http://127.0.0.1:9/hook") {
		t.Fatal("varsayılanda dahili hedef reddedilmeli")
	}
	if !ValidTarget("https://hooks.slack.com/services/x") {
		t.Fatal("genel hedef kabul edilmeli")
	}
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true")
	if !ValidTarget("http://10.1.2.3/siem") {
		t.Fatal("WEBHOOK_ALLOW_PRIVATE=true iken dahili hedef kabul edilmeli")
	}
	if ValidTarget("ftp://10.1.2.3/") {
		t.Fatal("http dışı şema her zaman reddedilmeli")
	}
}

// Gerçek HTTP alıcıya (loopback) gönderim: payload şekli, HTTP 2xx/4xx işleme.
func TestSendWebhook_DeliversDiscordSlackCompatiblePayload(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true")
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	payload := AlertPayload{Query: "leak", NewCount: 2, TotalCount: 5, RunAt: time.Now(), TopFindings: []Finding{
		{Title: "Fresh DB dump", URL: "http://a.onion/x", Category: "Veri Sızıntısı", Criticality: 5},
		{Title: "Forum post", URL: "http://b.onion/y", Category: "Siber Forum", Criticality: 3},
	}}
	if err := SendWebhook(srv.URL+"/hook", payload); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got["username"] != "KeywordHunter CTI" {
		t.Fatalf("username = %v", got["username"])
	}
	embeds, _ := got["embeds"].([]interface{})
	if len(embeds) != 1 {
		t.Fatalf("embeds len = %d", len(embeds))
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, "leak") || !strings.Contains(text, "Fresh DB dump") {
		t.Fatalf("text payload eksik: %q", text)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if err := SendWebhook(bad.URL, payload); err == nil {
		t.Fatal("HTTP 500 hata döndürmeli")
	}
	if err := SendWebhook("", payload); err != nil {
		t.Fatal("boş URL sessizce geçmeli")
	}
}

func TestSendWebhook_DoesNotFollowRedirects(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer srv.Close()
	err := SendWebhook(srv.URL, AlertPayload{Query: "x", RunAt: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("yönlendirme takip edilmemeli, 302 hata dönmeli: %v", err)
	}
}

func TestTopFindingsAndFilter(t *testing.T) {
	in := []Finding{{Criticality: 2}, {Criticality: 5}, {Criticality: 3}, {Criticality: 4}}
	top := TopFindings(in, 2)
	if len(top) != 2 || top[0].Criticality != 5 || top[1].Criticality != 4 {
		t.Fatalf("top = %+v", top)
	}
	if in[0].Criticality != 2 {
		t.Fatal("girdi değiştirilmemeli")
	}
	if f := FilterByThreshold(in, 4); len(f) != 2 {
		t.Fatalf("filter = %d", len(f))
	}
}
