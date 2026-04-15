/*
 * This file was last modified at 2026.04.15 20:40 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * SecurityContextPrincipalHelper.java
 * $Id$
 */

package su.svn.api.services.security;

import io.smallrye.jwt.auth.principal.DefaultJWTCallerPrincipal;
import io.smallrye.jwt.build.Jwt;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.core.SecurityContext;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.jwt.Claims;

import java.util.Set;
import java.util.UUID;

@ApplicationScoped
public class SecurityContextPrincipalHelper {

    private static final String GUEST_SUBJECT_UUID = "0f9233b1-7390-515d-9787-175006338642";
    private static final String GUEST_UPN = "guest";
    public static final String GUEST = "GUEST";

    @ConfigProperty(name = "application.external-jwt-api-key-prefix")
    String apiKeyPrefix;

    @ConfigProperty(name = "mp.jwt.verify.issuer")
    String mpJwtVerifyIssuer;

    @Inject
    SecurityContext securityContext;

    public String authorization() {
        return apiKeyPrefix + " " + this.insideToken();
    }

    public String insideToken() {
        try {
            if (securityContext.getUserPrincipal() instanceof DefaultJWTCallerPrincipal principal) {
                return Jwt.issuer(principal.getIssuer())
                        .subject(principal.getSubject())
                        .upn(principal.getName())
                        .groups(principal.getGroups())
                        .claim(Claims.jti, principal.getTokenID())
                        .sign();
            }
        } catch (Throwable ignored) {}
        return Jwt.issuer(mpJwtVerifyIssuer)
                .subject(GUEST_SUBJECT_UUID)
                .upn(GUEST_UPN)
                .groups(Set.of(GUEST))
                .claim(Claims.jti, UUID.randomUUID())
                .sign();
    }
}
