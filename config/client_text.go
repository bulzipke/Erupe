package config

const defaultLoginNotice = "<BODY><CENTER><SIZE_3><C_4>Welcome to Erupe!"

// LocalizedLoginNotices translates the built-in welcome text without changing
// any notices supplied by the operator. Sign-in precedes session localization.
func (c *Config) LocalizedLoginNotices() []string {
	notices := append([]string(nil), c.LoginNotices...)
	if c.Language == "ko" {
		for index, notice := range notices {
			if notice == defaultLoginNotice {
				notices[index] = "<BODY><CENTER><SIZE_3><C_4>Erupe에 오신 것을 환영합니다!"
			}
		}
	}
	return notices
}
