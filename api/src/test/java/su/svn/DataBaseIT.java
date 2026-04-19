package su.svn;

import io.quarkus.hibernate.reactive.panache.Panache;
import io.quarkus.test.InjectMock;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.vertx.RunOnVertxContext;
import io.quarkus.test.vertx.UniAsserter;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.junit.jupiter.api.Assertions;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.model.dto.PageRecordView;
import su.svn.api.model.dto.RecordView;
import su.svn.api.profile.ContainersProfile;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.services.domain.RecordDataService;

import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.Mockito.when;

@QuarkusTest
@QuarkusTestResource(value = PostgreSQLTestResource.class, restrictToAnnotatedClass = true)
@TestProfile(ContainersProfile.class)
public class DataBaseIT {

    public static final int CHUNK_SIZE = 1024;
    public static final int PAGE_SIZE = 127;
    public static final int ITERATION = 10;
    public static final double ITERATION_DOUBLE = ITERATION;
    public static final UUID ZERO_UUID = new UUID(0, 0);
    public static final UUID ONE_UUID = new UUID(0, 1);

    @Inject
    PostRecordRepository postRecordRepository;

    @Test
    @RunOnVertxContext
    void testPostRecord_findByUUID(UniAsserter asserter) {
        asserter.assertThat(() -> Panache.withTransaction(() ->
                        PostRecord.findByUUID(ZERO_UUID)
                ),
                postRecord -> {
                    assertNotNull(postRecord);
                    assertEquals(ZERO_UUID, postRecord.id());
                }
        );
    }

    @Test
    @RunOnVertxContext
    void testPostRecordRepository(UniAsserter asserter) {
        /* Liquibase will have run before this */
        OffsetDateTime odt = OffsetDateTime.now(ZoneId.systemDefault());
        ZoneOffset zoneOffset = odt.getOffset();
        for (int j = 0; j < ITERATION; j++) {
            var list = new ArrayList<PostRecord>();
            for (int i = 0; i < CHUNK_SIZE; i++) {
                var id = UUID.randomUUID();
                list.add(PostRecord.builder()
                        .id(id)
                        .parentId(id)
                        .userName("root")
                        .postAt(OffsetDateTime.of(LocalDateTime.now(), zoneOffset))
                        .lastChangedTime(LocalDateTime.now())
                        .build()
                );
            }
            asserter.execute(() -> postRecordRepository.persistAll(list));
        }
        var expectedPageCount = (int) Math.ceil(ITERATION_DOUBLE * CHUNK_SIZE / (double) PAGE_SIZE);
        var expectedPageIndex = 0;
        var expectedPageSize = PAGE_SIZE;
        asserter.assertThat(
                () -> postRecordRepository.readPage(expectedPageIndex, (byte) expectedPageSize),
                postRecords -> {
                    assertEquals(expectedPageCount, postRecords.pageCount());
                    assertEquals(expectedPageIndex, postRecords.pageIndex());
                    assertEquals(expectedPageSize, postRecords.pageSize());
                    assertEquals(expectedPageSize, postRecords.list().size());
                    assertTrue(postRecords.hasNextPage());
                    assertFalse(postRecords.hasPreviousPage());
                }
        );
        var expectedPageIndex2 = expectedPageCount - 1;
        var expectedPageSize2 = ITERATION * CHUNK_SIZE - expectedPageIndex2 * expectedPageSize + 1;
        asserter.assertThat(
                () -> postRecordRepository.readPage(expectedPageIndex2, (byte) expectedPageSize),
                postRecords -> {
                    assertEquals(expectedPageCount, postRecords.pageCount());
                    assertEquals(expectedPageIndex2, postRecords.pageIndex());
                    assertFalse(postRecords.hasNextPage());
                    assertTrue(postRecords.hasPreviousPage());
                }
        );
        var ids = new ArrayList<>(List.of(ZERO_UUID));
        var list = new ArrayList<PostRecord>();
        for (int i = 1; i < ITERATION; i++) {
            var id = new UUID(0, i);
            ids.add(id);
            list.add(PostRecord.builder()
                    .id(id)
                    .parentId(id)
                    .userName("root")
                    .postAt(OffsetDateTime.of(LocalDateTime.now(), zoneOffset))
                    .lastChangedTime(LocalDateTime.now())
                    .build()
            );
        }
        asserter.execute(() -> postRecordRepository.persistAll(list));
        asserter.assertThat(
                () -> postRecordRepository.readIdIn(ids),
                postRecords -> {
                    assertEquals(ITERATION, postRecords.size());
                    assertTrue(postRecords.stream().anyMatch(pr -> pr.id().equals(ZERO_UUID)));
                    for (long i = 1; i < ITERATION; i++) {
                        var j = i;
                        assertTrue(postRecords.stream().anyMatch(pr -> pr.id().equals(new UUID(0, j))));
                    }
                }
        );
        asserter.execute(() -> postRecordRepository.disable(ONE_UUID));
        asserter.assertThat(() -> Panache.withTransaction(() ->
                        PostRecord.findByUUID(ONE_UUID)
                ),
                postRecord -> {
                    assertNotNull(postRecord);
                    assertEquals(ONE_UUID, postRecord.id());
                    assertFalse(postRecord.enabled());
                }
        );
    }

    @InjectMock
    @RestClient
    RecordViewClient mockRecordViewClient;

    @Inject
    RecordDataService recordDataService;

    @BeforeEach
    void beforeEach() throws InterruptedException {
        OffsetDateTime odt = OffsetDateTime.now(ZoneId.systemDefault());
        ZoneOffset zoneOffset = odt.getOffset();

        var list = new ArrayList<RecordView>();
        var zeroUUID = UUID.fromString("00000000-0000-0000-0000-000000000000");
        list.add(RecordView.builder()
                .id(zeroUUID)
                .parentId(zeroUUID)
                .postAt(OffsetDateTime.of(LocalDateTime.now(), zoneOffset))
                .lastChangedTime(LocalDateTime.now())
                .flags(0)
                .build());
        for (int i = ITERATION; i < 2 * ITERATION; i++) {
            var id = new UUID(0, i);
            list.add(RecordView.builder()
                    .id(id)
                    .parentId(id)
                    .postAt(OffsetDateTime.of(LocalDateTime.now(), zoneOffset))
                    .lastChangedTime(LocalDateTime.now())
                    .flags(0)
                    .build()
            );
        }
        when(mockRecordViewClient.getByPageIndexAndSizeAndFromTimeAsUni(any(), anyInt(), anyInt(), any(), any(), any()))
                .thenReturn(Uni.createFrom().item(
                                PageRecordView.builder()
                                        .content(list)
                                        .last(true)
                                        .totalPages(1)
                                        .totalElements(1)
                                        .first(true)
                                        .number(0)
                                        .numberOfElements(list.size())
                                        .build()
                        )
                );
    }

    @Test
    @RunOnVertxContext
    void testRecordDataService(UniAsserter asserter) throws InterruptedException {
        asserter.assertThat(
                () -> recordDataService.sync(0, 2000)
                        .flatMap(postRecords -> postRecordRepository.readIdIn(
                                postRecords.stream().map(PostRecord::id).toList()
                        )),
                got -> {
                    assertEquals(ITERATION + 1, got.size());
                    for (long i = ITERATION; i < 2 * ITERATION; i++) {
                        var j = i;
                        assertTrue(got.stream().anyMatch(pr -> pr.id().equals(new UUID(0, j))));
                    }
                }
        );
    }

    @Test
    @RunOnVertxContext
    void testPostRecordRepository2(UniAsserter asserter) {
        asserter.assertThat(
                () -> postRecordRepository.findLastChangedTime(),
                Assertions::assertNotNull
        );
    }
}
