/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
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
import su.svn.api.domain.entities.RecordType;
import su.svn.api.model.dto.Page;
import su.svn.api.repository.client.rest.RecordViewClient;

import java.util.List;

@ApplicationScoped
public class RecordViewRepository {

    public static String SORT_PARAMS = "postAt%2CrefreshAt%2Cid%2Cdesc";

    @Inject
    @RestClient
    RecordViewClient recordViewClient;

    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        return recordViewClient.getByPageIndexAndSizeAsUni(pageIndex, size, SORT_PARAMS)
                .map(pageRecordView -> {
                    List<PostRecord> list = pageRecordView.content()
                            .stream()
                            .map(recordView -> PostRecord.builder()
                                    .id(recordView.id())
                                    .parentId(recordView.parentId())
                                    .type(RecordType.Base) // TODO
                                    .userName("root") // TODO
                                    .postAt(recordView.postAt())
                                    .refreshAt(recordView.refreshAt())
                                    .visible(recordView.visible())
                                    .flags(recordView.flags())
                                    .title(recordView.title())
                                    .values(recordView.values())
                                    .build()).toList();
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
}
