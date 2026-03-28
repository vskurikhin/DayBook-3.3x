/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PostRecord.java
 * $Id$
 */

package su.svn.api.domain.entities;

import io.quarkus.hibernate.reactive.panache.Panache;
import io.quarkus.hibernate.reactive.panache.PanacheEntityBase;
import io.quarkus.hibernate.reactive.panache.PanacheQuery;
import io.smallrye.mutiny.Uni;
import jakarta.annotation.Nonnull;
import jakarta.persistence.*;
import lombok.*;
import lombok.experimental.Accessors;
import lombok.experimental.FieldDefaults;
import org.hibernate.annotations.CreationTimestamp;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.annotations.UpdateTimestamp;
import org.hibernate.type.SqlTypes;

import java.io.Serializable;
import java.time.Duration;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

import static lombok.AccessLevel.PRIVATE;

@Accessors(fluent = true, chain = false)
@AllArgsConstructor
@Builder
@Entity
@EqualsAndHashCode(callSuper = false, exclude = {"id", "parent"})
@FieldDefaults(level = PRIVATE)
@Getter
@NoArgsConstructor
@Setter
@Table(schema = "api", name = "post_records")
@ToString(exclude = "parent")
@NamedQueries({
        @NamedQuery(
                name = PostRecord.READ_ENABLED_AND_ID_IN,
                query = """
                        FROM PostRecord
                        WHERE enabled = :enabled AND id IN :ids
                        ORDER BY postAt DESC, refreshAt DESC, id DESC
                        """
        ),
        @NamedQuery(
                name = PostRecord.READ_ENABLED_ORDER_POST_REFRESH_DESC,
                query = """
                        FROM PostRecord
                        WHERE enabled = :enabled
                        ORDER BY postAt DESC, refreshAt DESC, id DESC
                        """
        )
})
public class PostRecord extends PanacheEntityBase implements Serializable {

    public static final Duration TIMEOUT_DURATION = Duration.ofMillis(10000);
    public static final String READ_ENABLED_AND_ID_IN = "PostRecord.readEnabledAndIdIn";
    public static final String READ_ENABLED_ORDER_POST_REFRESH_DESC = "PostRecord.readEnabledOrderByPostAtAndRefreshAtDesc";
    public static final Map<String, Object> ENABLED = Map.of("enabled", Boolean.TRUE);

    @Column(name = "id", updatable = false, nullable = false)
    UUID id;

    @Column(name = "parent_id", nullable = false)
    UUID parentId;

    @ManyToOne(fetch = FetchType.LAZY)
    @JoinColumn(
            name = "parent_id",
            referencedColumnName = "id",
            insertable = false,
            nullable = false,
            updatable = false)
    PostRecord parent;

    @Id
    @GeneratedValue(strategy = GenerationType.SEQUENCE, generator = "postRecordSeq")
    @SequenceGenerator(
            name = "postRecordSeq",
            schema = "api",
            sequenceName = "post_records_seq",
            allocationSize = 1
    )
    @Column(name = "sequence_id", updatable = false, nullable = false)
    Long sequenceId;

    @Builder.Default
    @Column(name = "type", nullable = false)
    RecordType type = RecordType.Base;

    @Column(name = "user_name", nullable = false)
    String userName;

    @Column(name = "post_at", updatable = false, nullable = false)
    OffsetDateTime postAt;

    @Column(name = "refresh_at")
    OffsetDateTime refreshAt;

    @CreationTimestamp
    @Column(name = "create_time", updatable = false, nullable = false)
    LocalDateTime createTime;

    @UpdateTimestamp
    @Column(name = "update_time", updatable = false)
    LocalDateTime updateTime;

    @Builder.Default
    @Column(name = "enabled")
    boolean enabled = true;

    @Builder.Default
    @Column(name = "local_change", nullable = false)
    boolean localChange = true;

    @Column(name = "visible")
    Boolean visible;

    @Column(name = "flags", nullable = false)
    int flags;

    @Column(name = "title", columnDefinition = "TEXT")
    String title;

    @Column(name = "values")
    @JdbcTypeCode(SqlTypes.JSON)
    Map<String, String> values;

    public Uni<PostRecord> update() {
        return PostRecord.update(this);
    }

    public static Uni<PostRecord> create(@Nonnull PostRecord postRecord) {
        if (postRecord.parentId == null) {
            postRecord.parentId(postRecord.id());
            postRecord.parent(postRecord);
        }
        postRecord.flags &= (Integer.MAX_VALUE - 1);
        return Panache
                .withTransaction(postRecord::persistAndFlush)
                .replaceWith(postRecord)
                .ifNoItem()
                .after(TIMEOUT_DURATION)
                .fail()
                .onFailure()
                .transform(IllegalStateException::new);
    }

    public static Uni<Boolean> deleteById(@Nonnull UUID id) {
        return Panache.withTransaction(() -> PanacheEntityBase.deleteById(id));
    }

    public static Uni<PostRecord> findById(@Nonnull UUID id) {
        return PanacheEntityBase.findById(id);
    }

    public static Uni<List<PostRecord>> readEnabledAndIdIn(List<UUID> ids) {
        var params = new HashMap<>(PostRecord.ENABLED);
        params.put("ids", ids);
        return PostRecord.find("#" + READ_ENABLED_AND_ID_IN, params).list();
    }

    public static PanacheQuery<PostRecord> readEnabledOrderByPostAtAndRefreshAtDesc() {
        return PostRecord.find("#" + READ_ENABLED_ORDER_POST_REFRESH_DESC, PostRecord.ENABLED);
    }

    public static Uni<PostRecord> update(@Nonnull PostRecord postRecord) {
        return Panache
                .withTransaction(() -> PostRecord.findById(postRecord.id)
                        .onItem()
                        .ifNotNull()
                        .transform(entity -> {
                            entity.parentId = postRecord.parentId;
                            entity.type = postRecord.type;
                            entity.userName = postRecord.userName;
                            entity.enabled = postRecord.enabled;
                            entity.visible = postRecord.visible;
                            entity.flags = postRecord.flags &= (Integer.MAX_VALUE - 1);
                            return entity;
                        })
                        .onFailure()
                        .recoverWithNull()
                ).replaceWith(postRecord)
                .ifNoItem()
                .after(TIMEOUT_DURATION)
                .fail()
                .onFailure()
                .transform(IllegalStateException::new);
    }
}
