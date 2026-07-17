package app

import "testing"

func TestStorePersistsSettingsAndSent(t *testing.T) {
	path := t.TempDir() + "/data.json"
	s, err := loadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	v := defaults()
	v.ChannelID = "@test"
	if err := s.update(v); err != nil {
		t.Fatal(err)
	}
	if err := s.markSent("abc"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.get().ChannelID != "@test" || !reloaded.wasSent("abc") {
		t.Fatalf("not persisted: %#v", reloaded)
	}
}
