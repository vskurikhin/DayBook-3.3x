package su.svn.api.resources;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.junit.mockito.InjectSpy;
import io.smallrye.mutiny.Uni;
import io.vertx.core.Vertx;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.profile.PageReadIntegrationProfile;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.services.security.SecurityContextPrincipalHelper;

import java.util.concurrent.TimeUnit;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.Mockito.*;

@QuarkusTest
@TestProfile(PageReadIntegrationProfile.class)
class RecordsPageDatabaseIntegrationTest {
    @InjectSpy PostRecordRepository local;
    @InjectMock SecurityContextPrincipalHelper principal;
    PageReadIntegrationResource.CoreRequest core;

    @Test
    void realDatabaseWinsWhileCoreWaitsAndHeadersReachCore() throws Exception {
        when(principal.authorization()).thenReturn("Bearer fixture-token");
        doAnswer(invocation -> {
            @SuppressWarnings("unchecked") var actual = (Uni<Page<PostRecord>>) invocation.callRealMethod();
            var owner = Vertx.currentContext();
            // Wait for HTTP receipt, not time: the real DB query then proves it can win before Core responds.
            return Uni.createFrom().completionStage(core.received).ifNoItem().after(java.time.Duration.ofSeconds(2)).failWith(() -> new IllegalStateException("Core HTTP fixture was not reached"))
                    .emitOn(command -> owner.runOnContext(ignored -> command.run()))
                    .flatMap(ignored -> actual);
        }).when(local).readPage(0, (byte) 10);
        given().config(io.restassured.config.RestAssuredConfig.config().httpClient(
                io.restassured.config.HttpClientConfig.httpClientConfig()
                        .setParam("http.connection.timeout", 5000).setParam("http.socket.timeout", 5000))).header("X-Request-ID", "issue178-fixture").queryParam("page", 0).queryParam("size", 10)
                .when().get("/api/v2/records").then().statusCode(200)
                .body("list.size()", equalTo(1), "pageIndex", equalTo(0), "pageSize", equalTo(1),
                        "totalRecords", equalTo(1), "list[0].id", equalTo("00000000-0000-0000-0000-000000000000"));
        core.closed.get(5, TimeUnit.SECONDS);
        var request = core.received.get(5, TimeUnit.SECONDS);
        assertEquals("Bearer fixture-token", request.get("authorization"));
        assertEquals("issue178-fixture", request.get("requestId"));
        assertEquals("/core/api/v2/records-view", request.get("path"));
    }
}
