/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordView.java
 * $Id$
 */

package su.svn.core.domain.entities;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.FetchType;
import jakarta.persistence.GeneratedValue;
import jakarta.persistence.Id;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.Table;
import lombok.AllArgsConstructor;
import lombok.Builder;
import lombok.EqualsAndHashCode;
import lombok.Getter;
import lombok.NoArgsConstructor;
import lombok.Setter;
import lombok.ToString;
import lombok.experimental.Accessors;
import lombok.experimental.FieldDefaults;
import org.hibernate.annotations.CreationTimestamp;
import org.hibernate.annotations.Fetch;
import org.hibernate.annotations.FetchMode;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.annotations.UpdateTimestamp;
import org.hibernate.annotations.UuidGenerator;
import org.hibernate.type.SqlTypes;

import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

import static lombok.AccessLevel.PRIVATE;

@Accessors(fluent = true)
@AllArgsConstructor
@Builder
@Entity
@EqualsAndHashCode(callSuper = false, exclude = {"id"})
@FieldDefaults(level = PRIVATE)
@Getter
@NoArgsConstructor
@Setter
@Table(name="records_view", schema="core")
@ToString
public class RecordView {
    @Id
    @GeneratedValue
    @UuidGenerator
    @Column(name = "id", updatable = false, insertable = false)
    UUID id;

    @Column(name = "parent_id", nullable = false)
    UUID parentId;

    @ManyToOne(fetch = FetchType.EAGER)
    @JoinColumn(
            name = "parent_id",
            referencedColumnName = "id",
            insertable = false,
            nullable = false,
            updatable = false)
    @Fetch(FetchMode.JOIN)
    BaseRecord parent;

    @Builder.Default
    @Column(name = "type")
    RecordType type = RecordType.Base;

    @Column(name = "user_name", nullable = false)
    String userName;

    @Column(name = "post_at", nullable = false)
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
    @Column(name = "local_change")
    boolean localChange = true;

    @Column(name = "visible")
    boolean visible;

    @Column(name = "flags")
    int flags;

    @Column(name = "title")
    String title;

    @Column(name = "values")
    @JdbcTypeCode(SqlTypes.JSON)
    Map<String, String> values;
}
