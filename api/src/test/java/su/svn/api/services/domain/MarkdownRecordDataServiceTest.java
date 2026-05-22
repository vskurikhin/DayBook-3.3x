package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.helpers.test.UniAssertSubscriber;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.InjectMocks;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.NewMarkdownRecord;
import su.svn.api.models.dto.ResourceMarkdownRecord;
import su.svn.api.models.dto.UpdateMarkdownRecord;
import su.svn.api.repository.MarkdownRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.services.mappers.MarkdownRecordMapper;

import java.util.UUID;

import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class MarkdownRecordDataServiceTest {

    @Mock
    MarkdownRecordRepository recordRepository;

    @Mock
    MarkdownRecordMapper mapper;

    @Mock
    PostRecordRepository postRecordRepository;

    @InjectMocks
    MarkdownRecordDataService service;

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
        var dto = mock(NewMarkdownRecord.class);
        var response = mock(ResourceMarkdownRecord.class);

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
        var update = mock(UpdateMarkdownRecord.class);
        var resource = mock(ResourceMarkdownRecord.class);
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