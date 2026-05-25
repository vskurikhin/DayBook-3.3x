package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewXmlRecord;
import su.svn.api.models.dto.ResourceXmlRecord;
import su.svn.api.models.dto.UpdateXmlRecord;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.XmlRecordRepository;
import su.svn.api.services.mappers.XmlRecordMapper;

import java.time.OffsetDateTime;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class XmlRecordDataServiceTest {

    @InjectMocks
    XmlRecordDataService service;

    @Mock
    XmlRecordRepository recordRepository;

    @Mock
    XmlRecordMapper mapper;

    @Mock
    PostRecordRepository postRecordRepository;

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
    void shouldCreateRecord() {
        var request = NewXmlRecord.builder()
                .parentId(UUID.randomUUID())
                .title("xml")
                .xml("<root/>")
                .postAt(OffsetDateTime.now())
                .build();

        var response = ResourceXmlRecord.builder()
                .id(UUID.randomUUID())
                .xml("<root/>")
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
    void shouldUpdateRecord() {
        var request = UpdateXmlRecord.builder()
                .id(UUID.randomUUID())
                .xml("<updated/>")
                .refreshAt(OffsetDateTime.now())
                .build();

        var entity = PostRecord.builder()
                .id(request.id())
                .xml("<updated/>")
                .build();

        var response = ResourceXmlRecord.builder()
                .id(request.id())
                .xml("<updated/>")
                .build();

        when(recordRepository.put(request))
                .thenReturn(Uni.createFrom().item(response));

        when(mapper.toEntity(request))
                .thenReturn(entity);

        when(postRecordRepository.update(entity))
                .thenReturn(Uni.createFrom().item(entity));

        when(mapper.toResource(entity))
                .thenReturn(response);

        var result = service.put(request)
                .await()
                .indefinitely();

        assertThat(result).isEqualTo(response);

        verify(recordRepository).put(request);
        verify(postRecordRepository).update(entity);
        verify(mapper).toResource(entity);
    }
}