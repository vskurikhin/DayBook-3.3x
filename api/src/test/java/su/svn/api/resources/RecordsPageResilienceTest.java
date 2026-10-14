package su.svn.api.resources;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.subscription.UniEmitter;
import io.vertx.core.Vertx;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.profile.NoContainersProfile;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.RecordViewRepository;

import java.net.UnknownHostException;
import java.util.List;
import java.util.concurrent.atomic.AtomicReference;

import static io.restassured.RestAssured.given;
import static org.hamcrest.Matchers.equalTo;
import static org.mockito.Mockito.*;

/** HTTP mapper regression retaining the real coordination service. */
@QuarkusTest
@TestProfile(NoContainersProfile.class)
class RecordsPageResilienceTest {
    @InjectMock PostRecordRepository local;
    @InjectMock RecordViewRepository remote;

    @Test
    void dnsFailureBeforeLocalSuccessReturnsUnchangedPage() {
        var emitter = new AtomicReference<UniEmitter<? super Page<PostRecord>>>();
        when(local.readPage(2, (byte) 10)).thenAnswer(call -> Uni.createFrom().<Page<PostRecord>>emitter(emitter::set));
        when(remote.readPage(2, (byte) 10)).thenAnswer(call -> Uni.createFrom().<Page<PostRecord>>emitter(response -> {
            response.fail(new UnknownHostException("core.invalid"));
            Vertx.currentContext().runOnContext(ignored -> emitter.get().complete(new Page<>(List.of(), 3, 2, 0, 20)));
        }));
        given().config(io.restassured.config.RestAssuredConfig.config().httpClient(
                io.restassured.config.HttpClientConfig.httpClientConfig()
                        .setParam("http.connection.timeout", 5000).setParam("http.socket.timeout", 5000))).queryParam("page", 2).queryParam("size", 10)
                .when().get("/api/v2/records").then().statusCode(200)
                .body("pageCount", equalTo(3), "pageIndex", equalTo(2), "pageSize", equalTo(0),
                        "totalRecords", equalTo(20), "list.size()", equalTo(0));
        verify(local).readPage(2, (byte) 10);
        verify(remote).readPage(2, (byte) 10);
    }

    @Test
    void bothFailuresKeepGeneric500Body() {
        when(local.readPage(0, (byte) 10)).thenReturn(Uni.createFrom().failure(new IllegalStateException("db")));
        when(remote.readPage(0, (byte) 10)).thenReturn(Uni.createFrom().failure(new UnknownHostException("core.invalid")));
        given().config(io.restassured.config.RestAssuredConfig.config().httpClient(
                io.restassured.config.HttpClientConfig.httpClientConfig()
                        .setParam("http.connection.timeout", 5000).setParam("http.socket.timeout", 5000))).queryParam("page", 0).queryParam("size", 10)
                .when().get("/api/v2/records").then().statusCode(500)
                .body(equalTo("HTTP 500 Oops, something went wrong"));
    }
}
