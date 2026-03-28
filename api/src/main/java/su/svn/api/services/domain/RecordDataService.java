/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.model.dto.Page;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.RecordViewRepository;

@ApplicationScoped
public class RecordDataService {

    @Inject
    PostRecordRepository postRecordRepository;
    @Inject
    RecordViewRepository recordViewRepository;

    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        return Uni.combine()
                .any()
                .of(postRecordRepository.readPage(pageIndex, size), recordViewRepository.readPage(pageIndex, size));
    }
}
