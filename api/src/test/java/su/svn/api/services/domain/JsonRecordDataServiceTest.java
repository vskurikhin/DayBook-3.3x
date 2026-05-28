package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewJsonRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.models.dto.ResourceJsonRecord;
import su.svn.api.models.dto.UpdateJsonRecord;
import su.svn.api.repository.JsonRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.RecordViewRepository;
import su.svn.api.services.mappers.ExistingPostRecordMapper;
import su.svn.api.services.mappers.JsonRecordMapper;
import su.svn.lib.RecordType;

import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class JsonRecordDataServiceTest {

    @Mock
    JsonRecordRepository jsonRecordRepository;

    @Mock
    JsonRecordMapper jsonRecordMapper;

    @Mock
    ExistingPostRecordMapper existingPostRecordMapper;

    @Mock
    PostRecordRepository postRecordRepository;

    @Mock
    RecordViewRepository recordViewRepository;

    @InjectMocks
    JsonRecordDataService jsonRecordDataService;

    UUID id;
    PostRecord postRecord;
    ResourceJsonRecord resourceJsonRecord;

    @BeforeEach
    void setUp() {

        id = UUID.randomUUID();

        postRecord = PostRecord.builder()
                .id(id)
                .parentId(UUID.randomUUID())
                .type(RecordType.Json)
                .userName("root")
                .title("title")
                .json(Map.of("key", "value"))
                .enabled(true)
                .visible(true)
                .flags(1)
                .postAt(OffsetDateTime.now(ZoneOffset.UTC))
                .refreshAt(OffsetDateTime.now(ZoneOffset.UTC))
                .lastChangedTime(LocalDateTime.now())
                .build();

        resourceJsonRecord = ResourceJsonRecord.builder()
                .id(id)
                .title("title")
                .json(Map.of("key", "value"))
                .build();
    }

    @Test
    void shouldDeleteRecord() {

        when(jsonRecordRepository.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        when(postRecordRepository.disable(id))
                .thenReturn(Uni.createFrom().voidItem());

        Void result = jsonRecordDataService.delete(id)
                .await().indefinitely();

        assertThat(result).isNull();

        verify(jsonRecordRepository).delete(id);
        verify(postRecordRepository).disable(id);
    }

    @Test
    void shouldPostRecord() {

        NewJsonRecord request = NewJsonRecord.builder()
                .parentId(UUID.randomUUID())
                .title("title")
                .json(Map.of("key", "value"))
                .postAt(OffsetDateTime.now())
                .build();

        when(jsonRecordRepository.post(request))
                .thenReturn(Uni.createFrom().item(resourceJsonRecord));

        ResourceJsonRecord result = jsonRecordDataService.post(request)
                .await().indefinitely();

        assertThat(result).isNotNull();
        assertThat(result.id()).isEqualTo(id);
        assertThat(result.title()).isEqualTo("title");

        verify(jsonRecordRepository).post(request);
    }

    @Test
    void shouldUpdateRecord() {

        UpdateJsonRecord request = UpdateJsonRecord.builder()
                .id(id)
                .parentId(UUID.randomUUID())
                .title("updated")
                .refreshAt(OffsetDateTime.now())
                .build();

        when(jsonRecordRepository.put(request))
                .thenReturn(Uni.createFrom().item(resourceJsonRecord));

        when(jsonRecordMapper.toEntity(request))
                .thenReturn(postRecord);

        when(postRecordRepository.update(postRecord))
                .thenReturn(Uni.createFrom().item(postRecord));

        when(jsonRecordMapper.toResource(postRecord))
                .thenReturn(resourceJsonRecord);

        ResourceJsonRecord result = jsonRecordDataService.put(request)
                .await().indefinitely();

        assertThat(result).isNotNull();
        assertThat(result.id()).isEqualTo(id);

        verify(jsonRecordRepository).put(request);
        verify(postRecordRepository).update(postRecord);
    }

    @Test
    void shouldReadPage() {

        Page<PostRecord> expectedPage = mock(Page.class);

        when(postRecordRepository.readPage(0, (byte) 10))
                .thenReturn(Uni.createFrom().item(expectedPage));

        Page<PostRecord> result = jsonRecordDataService.readPage(0, (byte) 10)
                .await().indefinitely();

        assertThat(result).isNotNull();
    }

    @Test
    void shouldSyncRecords() {

        List<PostRecord> changedRecords = List.of(postRecord);

        when(postRecordRepository.findLastChangedTime())
                .thenReturn(Uni.createFrom().item(LocalDateTime.now()));

        when(recordViewRepository.readList(anyInt(), anyInt(), any()))
                .thenReturn(Uni.createFrom().item(changedRecords));

        when(postRecordRepository.readIdIn(any()))
                .thenReturn(Uni.createFrom().item(changedRecords));

        doReturn(Uni.createFrom().item(changedRecords))
                .when(postRecordRepository)
                .persistAll(any());

        List<PostRecord> result = jsonRecordDataService.sync(0, 10)
                .await().indefinitely();

        assertThat(result).isNotNull();
        assertThat(result).hasSize(1);

        verify(postRecordRepository).findLastChangedTime();
        verify(recordViewRepository).readList(anyInt(), anyInt(), any());
        verify(postRecordRepository).readIdIn(any());
        verify(postRecordRepository).persistAll(any());
        verify(existingPostRecordMapper).updateExistingRecord(any(), any());
    }

    @Test
    void shouldCreateNewJsonRecordBuilder() {

        OffsetDateTime postAt = OffsetDateTime.now();

        NewJsonRecord dto = NewJsonRecord.builder()
                .parentId(UUID.randomUUID())
                .title("title")
                .json(Map.of("k", "v"))
                .postAt(postAt)
                .visible(true)
                .flags(10)
                .build();

        assertThat(dto.title()).isEqualTo("title");
        assertThat(dto.postAt()).isEqualTo(postAt);
        assertThat(dto.flags()).isEqualTo(10);
        assertThat(dto.visible()).isTrue();
    }

    @Test
    void shouldCreateUpdateJsonRecordBuilder() {

        UUID id = UUID.randomUUID();
        OffsetDateTime refreshAt = OffsetDateTime.now();

        UpdateJsonRecord dto = UpdateJsonRecord.builder()
                .id(id)
                .parentId(UUID.randomUUID())
                .title("updated")
                .refreshAt(refreshAt)
                .visible(true)
                .flags(99)
                .build();

        assertThat(dto.id()).isEqualTo(id);
        assertThat(dto.title()).isEqualTo("updated");
        assertThat(dto.refreshAt()).isEqualTo(refreshAt);
        assertThat(dto.flags()).isEqualTo(99);
    }

    @Test
    void shouldBuildPostRecord() {

        UUID id = UUID.randomUUID();

        PostRecord record = PostRecord.builder()
                .id(id)
                .parentId(UUID.randomUUID())
                .type(RecordType.Json)
                .userName("root")
                .title("title")
                .enabled(true)
                .flags(5)
                .json(Map.of("a", "b"))
                .build();

        assertThat(record.id()).isEqualTo(id);
        assertThat(record.userName()).isEqualTo("root");
        assertThat(record.type()).isEqualTo(RecordType.Json);
        assertThat(record.flags()).isEqualTo(5);
        assertThat(record.json()).containsEntry("a", "b");
    }
}