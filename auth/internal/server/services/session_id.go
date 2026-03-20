package services

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	jwx "github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type sessionPrimaryKey struct {
	iss pgtype.UUID
	jti pgtype.UUID
	sub pgtype.UUID
}

type sessionID struct {
	issuerUUID     uuid.UUID
	userNameUUID   uuid.UUID
	sessionJTIUUID uuid.UUID
}

func newSessionID(hostname, UserName string) (sessionID, error) {
	sessionJTIUUID, err := uuid.NewV7()
	if err != nil {
		return sessionID{}, err
	}
	return sessionID{
		issuerUUID:     uuid.NewSHA1(uuid.NameSpaceDNS, []byte(hostname)),
		userNameUUID:   uuid.NewSHA1(uuid.NameSpaceDNS, []byte(UserName)),
		sessionJTIUUID: sessionJTIUUID,
	}, nil
}

func sessionIDFromClaims(claims jwt.Claims) (sessionID, error) {
	issuerUUID, errDecodeIssuer := tool.CanClaimUUID(claims.GetIssuer())
	if errDecodeIssuer != nil {
		return sessionID{}, errDecodeIssuer
	}
	userNameUUID, errDecodeSubject := tool.CanClaimUUID(claims.GetSubject())
	if errDecodeSubject != nil {
		return sessionID{}, errDecodeSubject
	}
	sessionJTIUUID, errDecodeJTI := tool.CanClaimUUID(tool.ExtractJTI(claims))
	if errDecodeJTI != nil {
		return sessionID{}, errDecodeJTI
	}
	return sessionID{
		issuerUUID:     issuerUUID,
		userNameUUID:   userNameUUID,
		sessionJTIUUID: sessionJTIUUID,
	}, nil
}

func sessionIDFromJwxToken(bearerToken jwx.Token) (sessionID, error) {
	issuerUUID, errDecodeIssuer := tool.CanUUIDParse(tool.JwxTokenIssuer(bearerToken))
	if errDecodeIssuer != nil {
		return sessionID{}, errDecodeIssuer
	}
	userNameUUID, errDecodeSubject := tool.CanUUIDParse(tool.JwxTokenSubject(bearerToken))
	if errDecodeSubject != nil {
		return sessionID{}, errDecodeSubject
	}
	sessionJTIUUID, errDecodeJTI := tool.CanUUIDParse(tool.JwxTokenJTI(bearerToken))
	if errDecodeJTI != nil {
		return sessionID{}, errDecodeJTI
	}
	return sessionID{
		issuerUUID:     issuerUUID,
		userNameUUID:   userNameUUID,
		sessionJTIUUID: sessionJTIUUID,
	}, nil
}

func (s sessionID) toModelPrimaryKey() sessionPrimaryKey {
	return sessionPrimaryKey{
		iss: pgtype.UUID{Bytes: s.issuerUUID, Valid: true},
		jti: pgtype.UUID{Bytes: s.sessionJTIUUID, Valid: true},
		sub: pgtype.UUID{Bytes: s.userNameUUID, Valid: true},
	}
}
