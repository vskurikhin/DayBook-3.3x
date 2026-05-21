/*
 * This file was last modified at 2026.05.21 16:48 by Victor N. Skurikhin.
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
import su.svn.api.repository.RecordViewRepository;
import su.svn.api.services.mappers.BlobPostRecordMapper;

import java.util.UUID;

@ApplicationScoped
public class BlobRecordDataService {

    @Inject
    BlobRecordRepository blobRecordRepository;

    @Inject
    BlobPostRecordMapper blobPostRecordMapper;

    @Inject
    PostRecordRepository postRecordRepository;

    @Inject
    RecordViewRepository recordViewRepository;

    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                blobRecordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    public Uni<ResourceBlobRecord> post(NewBlobRecord newBlobRecord) {
        return blobRecordRepository.post(newBlobRecord);
    }

    public Uni<ResourceBlobRecord> put(UpdateBlobRecord updateBlobRecord) {
        return blobRecordRepository.put(updateBlobRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(blobPostRecordMapper.toEntity(updateBlobRecord))
                                .map(postRecord -> blobPostRecordMapper.toResource(postRecord))
                );
    }
}
