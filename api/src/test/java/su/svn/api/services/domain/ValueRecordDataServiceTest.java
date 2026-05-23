package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.helpers.test.UniAssertSubscriber;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewValueRecord;
import su.svn.api.models.dto.ResourceValueRecord;
import su.svn.api.models.dto.UpdateValueRecord;
import su.svn.api.repository.ValueRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.services.mappers.ValueRecordMapper;

import java.util.UUID;

import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class ValueRecordDataServiceTest {

    @Mock
    ValueRecordRepository recordRepository;

    @Mock
    ValueRecordMapper mapper;

    @Mock
    PostRecordRepository postRecordRepository;

    @InjectMocks
    ValueRecordDataService service;

    @Test
    void shouldDeleteRecord() {
        var id = UUID.randomUUID();

        when(recordRepository.delete(id))
                .thenReturn(Uni.createFrom().voidItem());

        when(postRecordRepository.disable(id))
                .thenReturn(Uni.createFrom().voidItem());

        service.delete(id)
                .subscribe()
                .withSubscriber(UniAssertSubscriber.create())
                .assertCompleted();

        verify(recordRepository).delete(id);
        verify(postRecordRepository).disable(id);
    }

    @Test
    void shouldPostRecord() {
        var dto = mock(NewValueRecord.class);
        var response = mock(ResourceValueRecord.class);

        when(recordRepository.post(dto))
                .thenReturn(Uni.createFrom().item(response));

        service.post(dto)
                .subscribe()
                .withSubscriber(UniAssertSubscriber.create())
                .assertCompleted();

        verify(recordRepository).post(dto);
    }

    @Test
    void shouldPutRecord() {
        var update = mock(UpdateValueRecord.class);
        var resource = mock(ResourceValueRecord.class);
        var postRecord = mock(PostRecord.class);

        when(recordRepository.put(update))
                .thenReturn(Uni.createFrom().item(resource));

        when(mapper.toEntity(update))
                .thenReturn(postRecord);

        when(postRecordRepository.update(postRecord))
                .thenReturn(Uni.createFrom().item(postRecord));

        when(mapper.toResource(postRecord))
                .thenReturn(resource);

        service.put(update)
                .subscribe()
                .withSubscriber(UniAssertSubscriber.create())
                .assertCompleted();

        verify(recordRepository).put(update);
        verify(postRecordRepository).update(postRecord);
    }
}