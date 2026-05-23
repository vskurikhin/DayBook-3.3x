/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * SetRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.models.dto.NewSetRecord;
import su.svn.api.models.dto.ResourceSetRecord;
import su.svn.api.models.dto.UpdateSetRecord;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.SetRecordRepository;
import su.svn.api.services.mappers.SetRecordMapper;

import java.util.UUID;

@ApplicationScoped
public class SetRecordDataService {

    @Inject
    SetRecordRepository recordRepository;

    @Inject
    SetRecordMapper mapper;

    @Inject
    PostRecordRepository postRecordRepository;

    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    public Uni<ResourceSetRecord> post(NewSetRecord newSetRecord) {
        return recordRepository.post(newSetRecord);
    }

    public Uni<ResourceSetRecord> put(UpdateSetRecord updateSetRecord) {
        return recordRepository.put(updateSetRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(mapper.toEntity(updateSetRecord))
                                .map(postRecord -> mapper.toResource(postRecord))
                );
    }
}
