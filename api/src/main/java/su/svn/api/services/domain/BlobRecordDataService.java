/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * BlobRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.models.dto.NewBlobRecord;
import su.svn.api.models.dto.ResourceBlobRecord;
import su.svn.api.models.dto.UpdateBlobRecord;
import su.svn.api.repository.BlobRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.services.mappers.BlobRecordMapper;

import java.util.UUID;

@ApplicationScoped
public class BlobRecordDataService {

    @Inject
    BlobRecordRepository recordRepository;

    @Inject
    BlobRecordMapper mapper;

    @Inject
    PostRecordRepository postRecordRepository;

    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    public Uni<ResourceBlobRecord> post(NewBlobRecord newBlobRecord) {
        return recordRepository.post(newBlobRecord);
    }

    public Uni<ResourceBlobRecord> put(UpdateBlobRecord updateRecord) {
        return recordRepository.put(updateRecord)
                .flatMap(record -> postRecordRepository.update(mapper.toEntity(record))
                        .map(postRecord -> mapper.toResource(postRecord))
                );
    }
}
