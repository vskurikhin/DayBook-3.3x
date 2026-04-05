/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewRepository.java
 * $Id$
 */

package su.svn.api.repository;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.model.dto.Page;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.services.mappers.PostRecordMapper;

import java.time.LocalDateTime;
import java.util.List;

@ApplicationScoped
public class RecordViewRepository {

    public static String SORT_LIST_PARAMS = "lastChangedTime%2Cid%2Cdesc";
    public static String SORT_PAGE_PARAMS = "postAt%2CrefreshAt%2Cid%2Cdesc";

    @Inject
    PostRecordMapper postRecordMapper;

    @Inject
    @RestClient
    RecordViewClient recordViewClient;

    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        return recordViewClient.getByPageIndexAndSizeAsUni(pageIndex, size, SORT_PAGE_PARAMS)
                .map(pageRecordView -> {
                    var list = pageRecordView.content()
                            .stream()
                            .map(postRecordMapper::toEntity)
                            .toList();
                    return new Page<>(
                            list,
                            pageRecordView.totalPages(),
                            pageRecordView.number(),
                            pageRecordView.size(),
                            !pageRecordView.last(),
                            !pageRecordView.first()
                    );
                });
    }

    public Uni<List<PostRecord>> readList(int pageIndex, int size, LocalDateTime fromTime) {
        return recordViewClient.getByPageIndexAndSizeAndFromTimeAsUni(pageIndex, size, SORT_LIST_PARAMS, fromTime, true)
                .map(pageRecordView -> pageRecordView.content()
                        .stream()
                        .map(postRecordMapper::toEntity)
                        .toList());
    }
}
