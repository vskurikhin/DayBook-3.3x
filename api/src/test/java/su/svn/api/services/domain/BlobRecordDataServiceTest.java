package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewBlobRecord;
import su.svn.api.models.dto.ResourceBlobRecord;
import su.svn.api.models.dto.UpdateBlobRecord;
import su.svn.api.repository.BlobRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.RecordViewRepository;
import su.svn.api.services.mappers.BlobPostRecordMapper;

import java.time.OffsetDateTime;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class BlobRecordDataServiceTest {

    @InjectMocks
    BlobRecordDataService service;

    @Mock
    BlobRecordRepository blobRecordRepository;

    @Mock
    BlobPostRecordMapper blobPostRecordMapper;

    @Mock
    PostRecordRepository postRecordRepository;

    @Mock
    RecordViewRepository recordViewRepository;

    @Test
    void shouldDeleteRecord() {
        var id = UUID.randomUUID();

        when(blobRecordRepository.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        when(postRecordRepository.disable(id))
                .thenReturn(Uni.createFrom().voidItem());

        service.delete(id)
                .await().indefinitely();

        verify(blobRecordRepository).delete(id);
        verify(postRecordRepository).disable(id);
    }

    @Test
    void shouldPostRecord() {
        var request = NewBlobRecord.builder()
                .title("test")
                .postAt(OffsetDateTime.now())
                .build();

        var response = ResourceBlobRecord.builder()
                .id(UUID.randomUUID())
                .title("test")
                .build();

        when(blobRecordRepository.post(request))
                .thenReturn(Uni.createFrom().item(response));

        var result = service.post(request)
                .await().indefinitely();

        assertThat(result).isEqualTo(response);

        verify(blobRecordRepository).post(request);
    }

    @Test
    void shouldPutRecord() {
        var id = UUID.randomUUID();

        var request = UpdateBlobRecord.builder()
                .id(id)
                .refreshAt(OffsetDateTime.now())
                .build();

        var repositoryResponse = ResourceBlobRecord.builder()
                .id(id)
                .build();

        var entity = PostRecord.builder().build();

        var mappedResponse = ResourceBlobRecord.builder()
                .id(id)
                .title("updated")
                .build();

        when(blobRecordRepository.put(request))
                .thenReturn(Uni.createFrom().item(repositoryResponse));

        when(blobPostRecordMapper.toEntity(request))
                .thenReturn(entity);

        when(postRecordRepository.update(entity))
                .thenReturn(Uni.createFrom().item(entity));

        when(blobPostRecordMapper.toResource(entity))
                .thenReturn(mappedResponse);

        var result = service.put(request)
                .await().indefinitely();

        assertThat(result).isEqualTo(mappedResponse);

        verify(blobRecordRepository).put(request);
        verify(blobPostRecordMapper).toEntity(request);
        verify(postRecordRepository).update(entity);
        verify(blobPostRecordMapper).toResource(entity);
    }
}