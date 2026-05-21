/*
 * This file was last modified at 2026.05.21 16:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewClient.java
 * $Id$
 */

package su.svn.api.repository.client.rest;

import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.HeaderParam;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.QueryParam;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;
import su.svn.api.models.dto.PagedModelEntityModelResourceRecordView;

import java.time.LocalDateTime;

@Path("/core/api/v2/records-view")
@RegisterRestClient
public interface RecordViewClient {
    @GET
    Uni<PagedModelEntityModelResourceRecordView> getByPageIndexAndSizeAsUni(
            @HeaderParam("Authorization") String authorization,
            @HeaderParam("X-Request-ID") String requestId,
            @QueryParam("page") int pageIndex,
            @QueryParam("size") int size,
            @QueryParam("sort") String sort
    );

    @GET
    Uni<PagedModelEntityModelResourceRecordView> getByPageIndexAndSizeAndFromTimeAsUni(
            @HeaderParam("Authorization") String authorization,
            @HeaderParam("X-Request-ID") String requestId,
            @QueryParam("page") int pageIndex,
            @QueryParam("size") int size,
            @QueryParam("sort") String sort,
            @QueryParam("fromTime") LocalDateTime fromTime,
            @QueryParam("withDisabled") Boolean withDisabled
    );
}
