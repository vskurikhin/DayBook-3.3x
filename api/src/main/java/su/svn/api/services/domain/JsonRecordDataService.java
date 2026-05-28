/*
 * This file was last modified at 2026.05.28 18:28 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.quarkus.hibernate.reactive.panache.common.WithTransaction;
import io.smallrye.mutiny.Uni;
import jakarta.annotation.Nonnull;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.jboss.logging.Logger;
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

import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Function;
import java.util.stream.Collectors;

@ApplicationScoped
public class JsonRecordDataService {

    private static final Logger LOG = Logger.getLogger(JsonRecordDataService.class);

    @Inject
    JsonRecordRepository recordRepository;

    @Inject
    JsonRecordMapper mapper;

    @Inject
    PostRecordRepository postRecordRepository;

    @Inject
    ExistingPostRecordMapper existingPostRecordMapper;

    @Inject
    RecordViewRepository recordViewRepository;

    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    public Uni<ResourceJsonRecord> post(NewJsonRecord newJsonRecord) {
        return recordRepository.post(newJsonRecord);
    }

    public Uni<ResourceJsonRecord> put(UpdateJsonRecord updateJsonRecord) {
        return recordRepository.put(updateJsonRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(mapper.toEntity(updateJsonRecord))
                                .map(postRecord -> mapper.toResource(postRecord))
                );
    }

    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        return Uni.combine()
                .any()
                .of(postRecordRepository.readPage(pageIndex, size), recordViewRepository.readPage(pageIndex, size));
    }

    @WithTransaction
    public Uni<List<PostRecord>> sync(int pageIndex, int size) {
        return postRecordRepository.findLastChangedTime()
                .flatMap(fromTime ->
                        recordViewRepository.readList(pageIndex, size, fromTime)
                                .flatMap(this::syncPostRecords)
                );
    }

    private Uni<List<PostRecord>> syncPostRecords(@Nonnull List<PostRecord> postRecords) {
        final Map<UUID, PostRecord> map = convertToMap(postRecords);
        return postRecordRepository.readIdIn(map.keySet().stream().toList())
                .map(pr0 -> pr0.stream().peek(exsistPostRecord -> {
                    PostRecord newItem = map.get(exsistPostRecord.id());
                    if (newItem != null) {
                        existingPostRecordMapper.updateExistingRecord(exsistPostRecord, newItem);
                        LOG.debugf("existingPostRecord = %s", exsistPostRecord);
                        map.put(exsistPostRecord.id(), exsistPostRecord);
                    }
                }).toList())
                .map(pr1 -> map.values().stream().toList())
                .flatMap(pr2 -> postRecordRepository.persistAll(pr2));
    }

    private Map<UUID, PostRecord> convertToMap(@Nonnull List<PostRecord> postRecords) {
        return postRecords.stream()
                .collect(Collectors.toMap(PostRecord::id, Function.identity()));
    }
}
