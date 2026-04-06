/*
 * This file was last modified at 2026.04.06 22:35 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PostRecord.java
 * $Id$
 */

package su.svn.api.domain.entities;

import io.quarkus.hibernate.reactive.panache.PanacheEntityBase;
import io.quarkus.hibernate.reactive.panache.PanacheQuery;
import io.smallrye.mutiny.Uni;
import jakarta.annotation.Nonnull;
import jakarta.persistence.*;
import lombok.*;
import lombok.experimental.Accessors;
import lombok.experimental.FieldDefaults;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.type.SqlTypes;

import java.io.Serializable;
import java.time.Duration;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.*;

import static lombok.AccessLevel.PRIVATE;

/**
 * Entity representing a post record within the system.
 *
 * <p>This class is a reactive Active Record entity based on Panache and is mapped to the
 * {@code api.post_records} table. It encapsulates metadata and state related to a post,
 * including lifecycle timestamps, visibility flags, and hierarchical relationships.</p>
 *
 * <h2>Key Features</h2>
 * <ul>
 *     <li>Supports parent-child relationships via {@code parentId}</li>
 *     <li>Tracks lifecycle timestamps such as creation, update, and last change time</li>
 *     <li>Provides soft-disable functionality via the {@code enabled} flag</li>
 *     <li>Stores additional dynamic attributes in JSON format ({@code values})</li>
 * </ul>
 *
 * <h2>Reactive Operations</h2>
 * <p>All database operations are implemented using reactive APIs (Mutiny {@code Uni}).</p>
 * <ul>
 *     <li>{@link #findByUUID(UUID)} – retrieves a record by its UUID</li>
 *     <li>{@link #findLastChangedTime()} – returns the most recent {@code lastChangedTime}</li>
 *     <li>{@link #disable(UUID)} – disables a record by setting {@code enabled = false}</li>
 *     <li>{@link #update(PostRecord)} – updates mutable fields of an existing record</li>
 * </ul>
 *
 * <h2>Named Queries</h2>
 * <ul>
 *     <li>{@value #FIND_FIND_BY_UUID} – find record by UUID</li>
 *     <li>{@value #FIND_LAST_CHANGED_TIME_POST_RECORD} – fetch latest changed record</li>
 *     <li>{@value #READ_ENABLED_AND_ID_IN} – fetch enabled records by ID list</li>
 *     <li>{@value #READ_ENABLED_ORDER_POST_REFRESH_DESC} – fetch enabled records ordered by timestamps</li>
 * </ul>
 *
 * <h2>Concurrency and Timeout</h2>
 * <p>Operations such as {@link #disable(UUID)} and {@link #update(PostRecord)} include timeout
 * handling via {@link #TIMEOUT_DURATION} to prevent indefinite waiting.</p>
 *
 * <h2>Notes</h2>
 * <ul>
 *     <li>This entity uses a sequence-based primary key ({@code sequenceId})</li>
 *     <li>{@code id} is a business identifier (UUID) and is not auto-generated</li>
 *     <li>Lazy loading is used for the parent relationship</li>
 * </ul>
 *
 * @see io.quarkus.hibernate.reactive.panache.PanacheEntityBase
 * @see java.util.UUID
 * @see io.smallrye.mutiny.Uni
 */
@Accessors(fluent = true)
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
                name = PostRecord.FIND_FIND_BY_UUID,
                query = "FROM PostRecord WHERE id = :id"
        ),
        @NamedQuery(
                name = PostRecord.FIND_LAST_CHANGED_TIME_POST_RECORD,
                query = """
                        FROM PostRecord
                        ORDER BY lastChangedTime DESC, id DESC
                        """
        ),
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

    public static final Duration TIMEOUT_DURATION = Duration.ofMillis(2000);
    public static final String FIND_FIND_BY_UUID = "PostRecord.findByUUID";
    public static final String FIND_LAST_CHANGED_TIME_POST_RECORD = "PostRecord.findLastChangedTimePostRecord";
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

    @Column(name = "create_time", updatable = false)
    LocalDateTime createTime;

    @Column(name = "update_time")
    LocalDateTime updateTime;

    @Column(name = "last_changed_time", nullable = false)
    LocalDateTime lastChangedTime;

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

    public static Uni<PostRecord> findByUUID(@Nonnull UUID id) {
        return find("#" + FIND_FIND_BY_UUID, Map.of("id", id)).firstResult();
    }

    public static Uni<LocalDateTime> findLastChangedTime() {
        return PostRecord.findLastChangedTimePostRecord()
                .map(postRecord -> postRecord.lastChangedTime);
    }

    public static Uni<PostRecord> findLastChangedTimePostRecord() {
        return PostRecord.find("#" + FIND_LAST_CHANGED_TIME_POST_RECORD)
                .page(0, 1)
                .firstResult();
    }

    public static Uni<Void> disable(@Nonnull UUID id) {
        return findByUUID(id)
                .onItem()
                .ifNotNull()
                .transform(entity -> {
                    entity.enabled = false;
                    entity.lastChangedTime(LocalDateTime.now());
                    return entity;
                })
                .flatMap(postRecord -> persist(postRecord))
                .onItem()
                .transformToUni(postRecord -> Uni.createFrom().voidItem())
                .ifNoItem()
                .after(TIMEOUT_DURATION)
                .fail()
                .onFailure()
                .recoverWithUni(Uni.createFrom().voidItem());
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
        return findByUUID(postRecord.id)
                .onItem()
                .ifNotNull()
                .transform(entity -> {
                    entity.parentId(postRecord.parentId);
                    entity.type(postRecord.type);
                    entity.userName(postRecord.userName);
                    entity.refreshAt(postRecord.refreshAt);
                    entity.lastChangedTime(LocalDateTime.now());
                    entity.enabled(postRecord.enabled);
                    entity.visible(postRecord.visible);
                    entity.flags(postRecord.flags);
                    entity.title(postRecord.title);
                    entity.values(new LinkedHashMap<>(postRecord.values));
                    return entity;
                })
                .ifNoItem()
                .after(TIMEOUT_DURATION)
                .fail()
                .onFailure()
                .transform(IllegalStateException::new);
    }
}
