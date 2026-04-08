/*
 * This file was last modified at 2026.04.09 00:11 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * CustomJWTCallerPrincipalFactory.java
 * $Id$
 */

package su.svn.api.services.security;

import io.jsonwebtoken.Jwts;
import io.jsonwebtoken.security.Keys;
import io.smallrye.jwt.auth.principal.*;
import io.smallrye.jwt.build.Jwt;
import jakarta.annotation.Priority;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Alternative;
import lombok.SneakyThrows;
import org.eclipse.microprofile.jwt.Claims;
import su.svn.api.model.dto.UserHasRoles;
import su.svn.lib.auth.ApiException;
import su.svn.lib.auth.Configuration;
import su.svn.lib.auth.api.SessionApi;
import su.svn.lib.auth.auth.ApiKeyAuth;

import java.util.HashSet;

@ApplicationScoped
@Alternative
@Priority(1)
public class CustomJWTCallerPrincipalFactory extends JWTCallerPrincipalFactory {

    private static final String SECRET = "Fdh9u8rINxfivbrianbbVT1u232VQBZYKx1HGAGPt2I";

    private final DefaultJWTTokenParser defaultParser = new DefaultJWTTokenParser();

    @SneakyThrows
    @Override
    public JWTCallerPrincipal parse(String token, JWTAuthContextInfo authContextInfo) {
        System.out.println("token = " + token);
        System.out.println("authContextInfo = " + authContextInfo);
        var jwtParser = Jwts.parser()
                .verifyWith(Keys.hmacShaKeyFor((SECRET.getBytes())))
                .build();
        var jwt = jwtParser.parseSignedClaims(token);
        var claims = jwt.getPayload();

        System.out.println("claims.getIssuer() = " + claims.getIssuer());
        System.out.println("claims.getSubject() = " + claims.getSubject());
        System.out.println("claims.getId() = " + claims.getId());

        var userHasRoles = roles(token);
        var inside = Jwt.issuer(claims.getIssuer())
                .subject(claims.getSubject())
                .upn(userHasRoles.userName())
                .groups(userHasRoles.roles())
                .claim(Claims.jti, claims.getId())
                .sign();

        System.out.println("inside = " + inside);

        // Custom logic: e.g., manual decoding, external DB lookups, or custom validation
        // You can return a custom subclass of JWTCallerPrincipal here
        try {
            var jwtContext = defaultParser.parse(inside, authContextInfo);
            System.out.println("jwtContext = " + jwtContext);
            String type = jwtContext.getJoseObjects().get(0).getHeader("typ");
            System.out.println("type = " + type);
            return new DefaultJWTCallerPrincipal(type, jwtContext.getJwtClaims());
        } catch (Exception e) {
            e.printStackTrace();
            throw e;
        }
    }

    public UserHasRoles roles(String token) throws ApiException {
        var defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath("http://localhost:64148/auth/api");
        defaultClient.setConnectTimeout(900);
        defaultClient.setReadTimeout(500);

        // Configure API key authorization: BearerAuth
        var bearerAuth = (ApiKeyAuth) defaultClient.getAuthentication("BearerAuth");
        bearerAuth.setApiKey(token);
        bearerAuth.setApiKeyPrefix("Bearer");

        var apiInstance = new SessionApi(defaultClient);
        try {
            var result = apiInstance.v2SessionRolesGet();
            if (result.getData() == null || result.getData().getUserName() == null) {
                throw new ApiException("empty data or user_name");
            }
            var roles = new HashSet<String>();
            if (result.getData().getRoles() != null) {
                roles.addAll(result.getData().getRoles());
            } else {
                roles.add("GUEST");
            }
            System.out.println(result);
            return UserHasRoles.builder()
                    .userName(result.getData().getUserName())
                    .roles(roles)
                    .build();
        } catch (ApiException e) {
            System.err.println("Exception when calling SessionApi#v2SessionRolesGet");
            System.err.println("Status code: " + e.getCode());
            System.err.println("Reason: " + e.getResponseBody());
            System.err.println("Response headers: " + e.getResponseHeaders());
            throw e;
        }
    }
}