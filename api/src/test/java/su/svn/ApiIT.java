package su.svn;

import io.quarkus.test.InjectMock;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.vertx.RunOnVertxContext;
import io.quarkus.test.vertx.UniAsserter;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.enums.ResourcePath;
import su.svn.api.model.dto.Page;
import su.svn.api.model.dto.PageRecordView;
import su.svn.api.model.dto.RecordView;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.services.domain.RecordDataService;

import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.UUID;

import static io.restassured.RestAssured.given;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.when;

@QuarkusTest
public class ApiIT {

    @InjectMock
    @RestClient
    RecordViewClient mockRecordViewClient;

    @InjectMock
    PostRecordRepository mockPostRecordRepository;

    @BeforeEach
    public void setUp() {
        OffsetDateTime odt = OffsetDateTime.now(ZoneId.systemDefault());
        ZoneOffset zoneOffset = odt.getOffset();

        when(mockRecordViewClient.getByPageIndexAndSizeAsUni(anyInt(), anyInt(), any()))
                .thenReturn(Uni.createFrom().item(
                                PageRecordView.builder()
                                        .content(List.of(
                                                RecordView.builder()
                                                        .id(new UUID(0, 1))
                                                        .parentId(new UUID(0, 2))
                                                        .postAt(OffsetDateTime.MIN.withMinute(0))
                                                        .flags(0)
                                                        .build()
                                        ))
                                        .last(true)
                                        .totalPages(1)
                                        .totalElements(1)
                                        .first(true)
                                        .number(0)
                                        .size(1)
                                        .build()
                        )
                );
        when(mockPostRecordRepository.readPage(anyInt(), anyByte())).thenReturn(Uni.createFrom().nothing());
    }


    @Inject
    RecordDataService recordViewRepository;

    @Test
    @RunOnVertxContext
    void tests(UniAsserter asserter) {
        asserter.assertThat(
                () -> recordViewRepository.readPage(0, (byte) 127),
                recordView -> {
                    System.out.println("RESULT = " + recordView);
                }
        );
    }

    @Test
    void testHelloEndpoint() {
        @SuppressWarnings("unchecked") Page<LinkedHashMap<String, Object>> resp = given()
                .when().get(ResourcePath.RECORD)
                .then()
                .statusCode(200)
                .extract()
                .as(Page.class);
        assertAll(
                () -> assertNotNull(resp),
                () -> assertEquals("00000000-0000-0000-0000-000000000001", resp.list().getFirst().get("id")),
                () -> assertEquals("00000000-0000-0000-0000-000000000002", resp.list().getFirst().get("parentId")),
                () -> assertEquals("Base", resp.list().getFirst().get("type")),
                () -> assertEquals("-999999999-01-01T00:00:00+18:00", resp.list().getFirst().get("postAt"))
        );
    }
}
