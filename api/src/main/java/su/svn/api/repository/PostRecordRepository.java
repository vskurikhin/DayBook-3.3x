/*
 * This file was last modified at 2026.04.03 20:02 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PostRecordRepository.java
 * $Id$
 */

package su.svn.api.repository;

import io.quarkus.hibernate.reactive.panache.common.WithSession;
import io.quarkus.hibernate.reactive.panache.common.WithTransaction;
import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.hibernate.reactive.mutiny.Mutiny;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.model.dto.Page;

import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class PostRecordRepository {

    public static int BATCH_SIZE = 1024;

    @Inject
    Mutiny.SessionFactory mutinySessionFactory;

    @WithTransaction
    public Uni<List<PostRecord>> persistAll(List<PostRecord> records) {
        return mutinySessionFactory.withSession(session -> session
                .setBatchSize(BATCH_SIZE)
                .persistAll(records.toArray(new Object[0]))
        ).replaceWith(records);
    }

    @WithTransaction
    public Uni<List<PostRecord>> readIdIn(List<UUID> ids) {
        return PostRecord.readEnabledAndIdIn(ids);
    }

    @WithSession
    public Uni<Page<PostRecord>> readPage(int pageIndex, byte size) {
        var query = PostRecord.readEnabledOrderByPostAtAndRefreshAtDesc();
        var page = query.page(pageIndex, size);
        var unis = Uni.combine().all().unis(
                page.list(),
                page.pageCount(),
                page.hasNextPage(),
                Uni.createFrom().item(page.hasPreviousPage())
        );
        return unis.asTuple().map(t4 ->
                new Page<>(t4.getItem1(), t4.getItem2(), pageIndex, t4.getItem1().size(), t4.getItem3(), t4.getItem4())
        );
    }
}
