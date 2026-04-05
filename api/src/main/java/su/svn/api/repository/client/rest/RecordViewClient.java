/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewClient.java
 * $Id$
 */

package su.svn.api.repository.client.rest;

import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.QueryParam;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;
import su.svn.api.model.dto.PageRecordView;

import java.time.LocalDateTime;

@Path("/core/api/v2/records-view")
@RegisterRestClient
public interface RecordViewClient {
    @GET
    Uni<PageRecordView> getByPageIndexAndSizeAsUni(
            @QueryParam("page") int pageIndex,
            @QueryParam("size") int size,
            @QueryParam("sort") String sort
    );

    @GET
    Uni<PageRecordView> getByPageIndexAndSizeAndFromTimeAsUni(
            @QueryParam("page") int pageIndex,
            @QueryParam("size") int size,
            @QueryParam("sort") String sort,
            @QueryParam("fromTime") LocalDateTime fromTime,
            @QueryParam("withDisabled") Boolean withDisabled
    );
}
