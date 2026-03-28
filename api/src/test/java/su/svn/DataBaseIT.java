package su.svn;

import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.vertx.RunOnVertxContext;
import io.quarkus.test.vertx.UniAsserter;
import jakarta.inject.Inject;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.repository.PostRecordRepository;

import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;

@QuarkusTest
@QuarkusTestResource(PostgreSQLTestResource.class)
public class DataBaseIT {

    public static final int CHUNK_SIZE = 1024;
    public static final int PAGE_SIZE = 127;
    public static final int ITERATION = 10;
    public static final double ITERATION_DOUBLE = ITERATION;
    @Inject
    PostRecordRepository postRecordRepository;

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
                    assertEquals(expectedPageSize2, postRecords.pageSize());
                    assertEquals(expectedPageSize2, postRecords.list().size());
                    assertFalse(postRecords.hasNextPage());
                    assertTrue(postRecords.hasPreviousPage());
                }
        );
        var zeroUUID = UUID.fromString("00000000-0000-0000-0000-000000000000");
        var ids = new ArrayList<>(List.of(zeroUUID));
        var list = new ArrayList<PostRecord>();
        for (int i = 1; i < ITERATION; i++) {
            var id = new UUID(0, i);
            ids.add(id);
            list.add(PostRecord.builder()
                    .id(id)
                    .parentId(id)
                    .userName("root")
                    .postAt(OffsetDateTime.of(LocalDateTime.now(), zoneOffset))
                    .build()
            );
        }
        asserter.execute(() -> postRecordRepository.persistAll(list));
        asserter.assertThat(
                () -> postRecordRepository.readIdIn(ids),
                postRecords -> {
                    assertEquals(ITERATION, postRecords.size());
                    assertTrue(postRecords.stream().anyMatch(pr -> pr.id().equals(zeroUUID)));
                    for (long i = 1; i < ITERATION; i++) {
                        var j = i;
                        assertTrue(postRecords.stream().anyMatch(pr -> pr.id().equals(new UUID(0, j))));
                    }
                }
        );
    }
}
