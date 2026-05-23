/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ValueRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.models.dto.NewValueRecord;
import su.svn.api.models.dto.ResourceValueRecord;
import su.svn.api.models.dto.UpdateValueRecord;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.ValueRecordRepository;
import su.svn.api.services.mappers.ValueRecordMapper;

import java.util.UUID;

@ApplicationScoped
public class ValueRecordDataService {

    @Inject
    ValueRecordRepository recordRepository;

    @Inject
    ValueRecordMapper mapper;

    @Inject
    PostRecordRepository postRecordRepository;

    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    public Uni<ResourceValueRecord> post(NewValueRecord newValueRecord) {
        return recordRepository.post(newValueRecord);
    }

    public Uni<ResourceValueRecord> put(UpdateValueRecord updateValueRecord) {
        return recordRepository.put(updateValueRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(mapper.toEntity(updateValueRecord))
                                .map(postRecord -> mapper.toResource(postRecord))
                );
    }
}
