package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewVectorRecord;
import su.svn.api.models.dto.ResourceVectorRecord;
import su.svn.api.models.dto.UpdateVectorRecord;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.VectorRecordRepository;
import su.svn.api.services.mappers.VectorRecordMapper;

import java.time.OffsetDateTime;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

@ExtendWith(MockitoExtension.class)
class VectorRecordDataServiceTest {

    @InjectMocks
    VectorRecordDataService service;

    @Mock
    VectorRecordRepository recordRepository;

    @Mock
    PostRecordRepository postRecordRepository;

    @Mock
    VectorRecordMapper mapper;

    @Test
    void shouldDeleteRecord() {
        var id = UUID.randomUUID();

        when(recordRepository.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        when(postRecordRepository.disable(id))
                .thenReturn(Uni.createFrom().voidItem());

        service.delete(id)
                .await()
                .indefinitely();

        verify(recordRepository).delete(id);
        verify(postRecordRepository).disable(id);
    }

    @Test
    void shouldPostRecord() {
        var request = NewVectorRecord.builder()
                .parentId(UUID.randomUUID())
                .title("vector")
                .vector(new float[]{1.0f, 2.0f})
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(5)
                .build();

        var response = ResourceVectorRecord.builder()
                .id(UUID.randomUUID())
                .title("vector")
                .vector(new float[]{1.0f, 2.0f})
                .visible(true)
                .flags(5)
                .build();

        when(recordRepository.post(request))
                .thenReturn(Uni.createFrom().item(response));

        var result = service.post(request)
                .await()
                .indefinitely();

        assertThat(result).isEqualTo(response);

        verify(recordRepository).post(request);
    }

    @Test
    void shouldPutRecord() {
        var id = UUID.randomUUID();

        var request = UpdateVectorRecord.builder()
                .id(id)
                .title("updated")
                .vector(new float[]{5.0f, 6.0f})
                .refreshAt(OffsetDateTime.now())
                .visible(true)
                .flags(9)
                .build();

        var repositoryResponse = ResourceVectorRecord.builder()
                .id(id)
                .title("updated")
                .vector(new float[]{5.0f, 6.0f})
                .visible(true)
                .flags(9)
                .build();

        var postRecord = PostRecord.builder()
                .id(id)
                .title("updated")
                .visible(true)
                .flags(9)
                .build();

        var mappedResponse = ResourceVectorRecord.builder()
                .id(id)
                .title("updated")
                .vector(new float[]{5.0f, 6.0f})
                .visible(true)
                .flags(9)
                .build();

        when(recordRepository.put(request))
                .thenReturn(Uni.createFrom().item(repositoryResponse));

        when(mapper.toEntity(request))
                .thenReturn(postRecord);

        when(postRecordRepository.update(postRecord))
                .thenReturn(Uni.createFrom().item(postRecord));

        when(mapper.toResource(postRecord))
                .thenReturn(mappedResponse);

        var result = service.put(request)
                .await()
                .indefinitely();

        assertThat(result).isEqualTo(mappedResponse);

        verify(recordRepository).put(request);
        verify(mapper).toEntity(request);
        verify(postRecordRepository).update(postRecord);
        verify(mapper).toResource(postRecord);
    }
}