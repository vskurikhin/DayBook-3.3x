/*
 * This file was last modified at 2026.04.15 20:40 by Victor N. Skurikhin.
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
import org.eclipse.microprofile.config.inject.ConfigProperty;
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

    public static final String BEARER_AUTH = "BearerAuth";
    public static final String GUEST = "GUEST";

    @ConfigProperty(name = "application.external-jwt-api-key-prefix")
    String apiKeyPrefix;

    @ConfigProperty(name = "application.auth-server-url")
    String authServerUrl;

    @ConfigProperty(name = "application.external-jwt-verify-key-hmac")
    String secret;

    @ConfigProperty(name = "application.auth-server-connect-timeout")
    int connectTimeout;

    @ConfigProperty(name = "application.auth-server-read-timeout")
    int readTimeout;

    private final DefaultJWTTokenParser defaultParser = new DefaultJWTTokenParser();

    @SneakyThrows
    @Override
    public JWTCallerPrincipal parse(String token, JWTAuthContextInfo authContextInfo) {
        System.out.println("secret = " + secret);
        System.out.println("authContextInfo = " + authContextInfo);
        var jwtParser = Jwts.parser()
                .verifyWith(Keys.hmacShaKeyFor(secret.getBytes()))
                .build();
        var jwt = jwtParser.parseSignedClaims(token);
        var claims = jwt.getPayload();
        var userHasRoles = roles(token);

        String inside = Jwt.issuer(claims.getIssuer())
                .subject(claims.getSubject())
                .upn(userHasRoles.userName())
                .groups(userHasRoles.roles())
                .claim(Claims.jti, claims.getId())
                .signWithSecret(secret);;

        // Custom logic: e.g., manual decoding, external DB lookups, or custom validation
        // You can return a custom subclass of JWTCallerPrincipal here
        try {
            var jwtContext = defaultParser.parse(inside, authContextInfo);
            System.out.println("jwtContext = " + jwtContext);
            String type = jwtContext.getJoseObjects().getFirst().getHeader("typ");
            System.out.println("type = " + type);
            return new DefaultJWTCallerPrincipal(type, jwtContext.getJwtClaims());
        } catch (Exception e) {
            e.printStackTrace();
            throw e;
        }
    }

    UserHasRoles roles(String token) throws ApiException {
        var apiInstance = getSessionApi(token);
        var result = apiInstance.v2SessionRolesGet();

        if (result.getData() == null || result.getData().getUserName() == null) {
            throw new ApiException("empty data or user_name");
        }
        var roles = new HashSet<String>();

        if (result.getData().getRoles() != null) {
            roles.addAll(result.getData().getRoles());
        } else {
            roles.add(GUEST);
        }
        return UserHasRoles.builder()
                .userName(result.getData().getUserName())
                .roles(roles)
                .build();
    }

    SessionApi getSessionApi(String token) {
        var defaultClient = Configuration.getDefaultApiClient();
        defaultClient.setBasePath(authServerUrl);
        defaultClient.setConnectTimeout(connectTimeout);
        defaultClient.setReadTimeout(readTimeout);

        // Configure API key authorization: BearerAuth
        var bearerAuth = (ApiKeyAuth) defaultClient.getAuthentication(BEARER_AUTH);
        bearerAuth.setApiKey(token);
        bearerAuth.setApiKeyPrefix(apiKeyPrefix);

        return new SessionApi(defaultClient);
    }
}