package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.RecordViewRepository;
import su.svn.api.services.mappers.ExistingPostRecordMapper;

import java.time.LocalDateTime;
import java.util.List;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

class PostRecordDataSyncServiceTest {

    private PostRecordRepository postRecordRepository;
    private ExistingPostRecordMapper mapper;
    private RecordViewRepository recordViewRepository;

    private PostRecordDataSyncService service;

    @BeforeEach
    void setUp() {
        postRecordRepository = mock(PostRecordRepository.class);
        mapper = mock(ExistingPostRecordMapper.class);
        recordViewRepository = mock(RecordViewRepository.class);

        service = new PostRecordDataSyncService();
        service.postRecordRepository = postRecordRepository;
        service.existingPostRecordMapper = mapper;
        service.recordViewRepository = recordViewRepository;
        service.recordsPageTimeout = new RecordsPageTimeout(1000);
    }

    @Test
    void dnsFailureMustKeepPendingLocalPageAlive() {
        var local = new java.util.concurrent.atomic.AtomicReference<io.smallrye.mutiny.subscription.UniEmitter<? super Page<PostRecord>>>();
        var cancelled = new java.util.concurrent.atomic.AtomicBoolean();
        var expected = new Page<PostRecord>(List.of(), 1, 0, 0, 0);
        when(postRecordRepository.readPage(0, (byte) 10)).thenReturn(
                Uni.createFrom().<Page<PostRecord>>emitter(local::set)
                        .onCancellation().invoke(() -> cancelled.set(true)));
        when(recordViewRepository.readPage(0, (byte) 10)).thenReturn(
                Uni.createFrom().failure(new java.net.UnknownHostException("core.invalid")));
        var result = service.readPage(0, (byte) 10).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        assertFalse(cancelled.get(), "Core DNS failure must not cancel pending local data");
        assertFalse(result.isDone());
        local.get().complete(expected);
        assertSame(expected, result.join());
    }

    @Test
    void shouldReadPage() {
        //noinspection unchecked
        Page<PostRecord> page = mock(Page.class);

        when(postRecordRepository.readPage(0, (byte) 10))
                .thenReturn(Uni.createFrom().item(page));

        when(recordViewRepository.readPage(0, (byte) 10))
                .thenReturn(Uni.createFrom().item(page));

        Object result = service.readPage(0, (byte) 10)
                .await()
                .indefinitely();

        assertNotNull(result);
    }

    @Test
    void shouldSyncRecords() {

        var time = LocalDateTime.now();

        UUID id = UUID.randomUUID();

        PostRecord incoming = mock(PostRecord.class);
        PostRecord existing = mock(PostRecord.class);

        when(incoming.id()).thenReturn(id);
        when(existing.id()).thenReturn(id);

        List<PostRecord> incomingList = List.of(incoming);
        List<PostRecord> existingList = List.of(existing);

        when(postRecordRepository.findLastChangedTime())
                .thenReturn(Uni.createFrom().item(time));

        when(recordViewRepository.readList(0, 10, time))
                .thenReturn(Uni.createFrom().item(incomingList));

        when(postRecordRepository.readIdIn(anyList()))
                .thenReturn(Uni.createFrom().item(existingList));

        when(postRecordRepository.persistAll(anyList()))
                .thenReturn(Uni.createFrom().item(incomingList));

        List<PostRecord> result = service.sync(0, 10)
                .await()
                .indefinitely();

        assertNotNull(result);

        verify(mapper)
                .updateExistingRecord(existing, incoming);

        verify(postRecordRepository)
                .persistAll(anyList());
    }

    @Test
    void shouldHandleEmptySyncList() {

        var time = LocalDateTime.now();

        when(postRecordRepository.findLastChangedTime())
                .thenReturn(Uni.createFrom().item(time));

        when(recordViewRepository.readList(0, 10, time))
                .thenReturn(Uni.createFrom().item(List.of()));

        when(postRecordRepository.readIdIn(anyList()))
                .thenReturn(Uni.createFrom().item(List.of()));

        when(postRecordRepository.persistAll(anyList()))
                .thenReturn(Uni.createFrom().item(List.of()));

        List<PostRecord> result = service.sync(0, 10)
                .await()
                .indefinitely();

        assertNotNull(result);
        verify(postRecordRepository)
                .persistAll(anyList());
    }

    @Test
    void shouldReadPostRecord() {

        UUID id = UUID.randomUUID();

        PostRecord record = mock(PostRecord.class);


        when(postRecordRepository.findById(id))
                .thenReturn(Uni.createFrom().item(record));


        when(recordViewRepository.readRecord(id))
                .thenReturn(Uni.createFrom().item(record));


        PostRecord result =
                service.readPostRecord(id)
                        .await()
                        .indefinitely();


        assertNotNull(result);

        verify(postRecordRepository)
                .findById(id);

        verify(recordViewRepository)
                .readRecord(id);
    }
}