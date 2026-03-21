package services

import "time"

type validTimeTokens struct {
	accessTokenTime   time.Time
	cookieExpiresTime time.Time
	refreshTokenTime  time.Time
	sessionValidTime  time.Time
}

func makeValidTimeTokens(validPeriodAccessToken, validPeriodRefreshToken time.Duration) validTimeTokens {
	return validTimeTokens{
		accessTokenTime:   time.Now().Add(validPeriodAccessToken),
		cookieExpiresTime: time.Now().Add(validPeriodRefreshToken).Add(time.Second),
		refreshTokenTime:  time.Now().Add(validPeriodRefreshToken),
		sessionValidTime:  time.Now().Add(validPeriodRefreshToken),
	}
}
