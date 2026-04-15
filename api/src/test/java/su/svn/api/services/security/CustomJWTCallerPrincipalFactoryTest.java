package su.svn.api.services.security;

import io.jsonwebtoken.security.Keys;
import io.quarkus.test.junit.QuarkusTest;
import io.smallrye.jwt.algorithm.SignatureAlgorithm;
import io.smallrye.jwt.auth.principal.JWTAuthContextInfo;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import su.svn.api.model.dto.UserHasRoles;
import su.svn.lib.auth.ApiException;
import su.svn.lib.auth.api.SessionApi;
import su.svn.lib.auth.model.V2SessionRolesGet200Response;

import java.util.List;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;


@QuarkusTest
class CustomJWTCallerPrincipalFactoryTest {

    CustomJWTCallerPrincipalFactory factory;

    @BeforeEach
    void setUp() {
        factory = Mockito.spy(new CustomJWTCallerPrincipalFactory());
    }

    @Test
    void roles_shouldReturnUserAndRoles() throws Exception {
        // given
        String token = "test-token";

        SessionApi api = mock(SessionApi.class);
        var response = mockResponse("john", List.of("ADMIN", "USER"));

        doReturn(api).when(factory).getSessionApi(token);
        when(api.v2SessionRolesGet()).thenReturn((V2SessionRolesGet200Response) response);

        // when
        UserHasRoles result = factory.roles(token);

        // then
        assertEquals("john", result.userName());
        assertTrue(result.roles().contains("ADMIN"));
        assertTrue(result.roles().contains("USER"));
    }

    @Test
    void roles_shouldReturnGuest_whenRolesNull() throws Exception {
        String token = "test-token";

        SessionApi api = mock(SessionApi.class);
        var response = mockResponse("john", null);

        doReturn(api).when(factory).getSessionApi(token);
        when(api.v2SessionRolesGet()).thenReturn((V2SessionRolesGet200Response) response);

        UserHasRoles result = factory.roles(token);

        assertEquals("john", result.userName());
        assertTrue(result.roles().contains(CustomJWTCallerPrincipalFactory.GUEST));
    }

    @Test
    void roles_shouldThrowException_whenUserNameNull() throws Exception {
        String token = "test-token";

        SessionApi api = mock(SessionApi.class);
        var response = mockResponse(null, List.of("ADMIN"));

        doReturn(api).when(factory).getSessionApi(token);
        when(api.v2SessionRolesGet()).thenReturn((V2SessionRolesGet200Response) response);

        assertThrows(ApiException.class, () -> factory.roles(token));
    }

    @Test
    void parse_shouldReturnPrincipal() throws Exception {
        CustomJWTCallerPrincipalFactory factory = Mockito.spy(new CustomJWTCallerPrincipalFactory());

        // config values
        factory.secret = "12345678901234567890123456789012"; // 32 bytes
        factory.apiKeyPrefix = "Bearer";
        factory.authServerUrl = "http://localhost";

        String token = generateTestToken(factory.secret);

        doReturn(UserHasRoles.builder()
                .userName("john")
                .roles(Set.of("USER"))
                .build()
        ).when(factory).roles(token);

        // var authContext = mock(io.smallrye.jwt.auth.principal.JWTAuthContextInfo.class);

        JWTAuthContextInfo authContextInfo = new JWTAuthContextInfo();

        // ВАЖНО 👇
        authContextInfo.setSecretVerificationKey(
                Keys.hmacShaKeyFor("12345678901234567890123456789012".getBytes())
        );
        // ВАЖНО: разрешаем HS256
        authContextInfo.setSignatureAlgorithm(Set.of(SignatureAlgorithm.HS256));

        var result = factory.parse(token, authContextInfo);

        assertNotNull(result);
        assertEquals("john", result.getName());
    }

    // helper
    private Object mockResponse(String userName, List<String> roles) {
        var response = mock(V2SessionRolesGet200Response.class, RETURNS_DEEP_STUBS);

        // deep stub
        assert response.getData() != null;
        when(response.getData().getUserName())
                .thenReturn(userName);

        when(response.getData().getRoles())
                .thenReturn(roles);

        return response;
    }

    private String generateTestToken(String secret) {
        return io.jsonwebtoken.Jwts.builder()
                .setSubject("123")
                .setIssuer("cd8f5f9f-e3e8-569f-87ef-f03c6cfc29bc")
                .setId("jti-1")
                .signWith(io.jsonwebtoken.security.Keys.hmacShaKeyFor(secret.getBytes()))
                .compact();
    }
}