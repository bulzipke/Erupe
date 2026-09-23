package migrations

import "testing"

func TestDivaSpecialPugiSchemaIndependentAndRecoverable(t *testing.T) {
	host := getEnv("TEST_DB_HOST", "localhost")
	if (host != "localhost" && host != "127.0.0.1") || getEnv("TEST_DB_PORT", "5433") != "5433" || getEnv("TEST_DB_NAME", "erupe_test") != "erupe_test" {
		t.Fatal("special-poogie migration tests require isolated localhost:5433/erupe_test")
	}
	db := testDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE guilds(id INTEGER PRIMARY KEY,pugi_outfit_1 SMALLINT NOT NULL DEFAULT 0);
		INSERT INTO guilds VALUES(1,12)`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFS.ReadFile("sql/0060_diva_special_pugi.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var defaults bool
	if err = db.Get(&defaults, `SELECT pugi_outfit_1=12 AND diva_pugi_outfit_1=0 AND diva_pugi_outfit_2=0 AND diva_pugi_outfit_3=0 FROM guilds WHERE id=1`); err != nil || !defaults {
		t.Fatal(defaults, err)
	}
	if _, err = db.Exec(`UPDATE guilds SET diva_pugi_outfit_1=3,diva_pugi_outfit_2=5,diva_pugi_outfit_3=9 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err = db.Get(&preserved, `SELECT pugi_outfit_1=12 AND diva_pugi_outfit_1=3 AND diva_pugi_outfit_2=5 AND diva_pugi_outfit_3=9 FROM guilds WHERE id=1`); err != nil || !preserved {
		t.Fatal(preserved, err)
	}
	for _, q := range []string{
		`UPDATE guilds SET diva_pugi_outfit_1=-1`,
		`UPDATE guilds SET diva_pugi_outfit_2=10`,
		`UPDATE guilds SET diva_pugi_outfit_3=NULL`,
	} {
		if _, err = db.Exec(q); err == nil {
			t.Fatal("invalid clothes accepted", q)
		}
	}
}
