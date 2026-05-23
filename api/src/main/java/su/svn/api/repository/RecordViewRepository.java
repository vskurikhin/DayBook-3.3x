/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
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
import org.slf4j.MDC;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.Page;
import su.svn.api.repository.client.rest.RecordViewClient;
import su.svn.api.services.mappers.JsonRecordMapper;
import su.svn.api.services.security.SecurityContextPrincipalHelper;

import java.time.LocalDateTime;
import java.util.List;

import static su.svn.lib.Constants.REQUEST_ID;

@ApplicationScoped
public class RecordViewRepository {

    public static String SORT_LIST_PARAMS = "lastChangedTime%2Cid%2Cdesc";
    public static String SORT_PAGE_PARAMS = "postAt%2CrefreshAt%2Cid%2Cdesc";

    @Inject
    JsonRecordMapper jsonRecordMapper;

    @Inject
    @RestClient
    RecordViewClient viewClient;

    @Inject
    SecurityContextPrincipalHelper principalHelper;

    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        var authorization = principalHelper.authorization();
        var requestId = MDC.get(REQUEST_ID);
        return viewClient.getByPageIndexAndSizeAsUni(authorization, requestId, pageIndex, size, SORT_PAGE_PARAMS)
                .map(pageRecordView -> {
                    var list = pageRecordView.embedded().resourceRecordViewList()
                            .stream()
                            .map(jsonRecordMapper::toEntity)
                            .toList();
                    return new Page<>(
                            list,
                            pageRecordView.page().totalPages(),
                            pageRecordView.page().number(),
                            pageRecordView.page().size()
                    );
                });
    }

    public Uni<List<PostRecord>> readList(int pageIndex, int size, LocalDateTime fromTime) {
        var authorization = principalHelper.authorization();
        var requestId = MDC.get(REQUEST_ID);
        return viewClient.getByPageIndexAndSizeAndFromTimeAsUni(
                authorization, requestId, pageIndex, size, SORT_LIST_PARAMS, fromTime, true
        ).map(pageRecordView -> pageRecordView.embedded().resourceRecordViewList()
                .stream()
                .map(jsonRecordMapper::toEntity)
                .toList());
    }
}
