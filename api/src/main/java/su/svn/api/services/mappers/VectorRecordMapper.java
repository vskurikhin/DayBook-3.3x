/*
 * This file was last modified at 2026.05.22 19:39 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * VectorRecordMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.EntityModelResourceRecordView;
import su.svn.api.models.dto.ResourceVectorRecord;
import su.svn.api.models.dto.UpdateVectorRecord;

/**
 * MapStruct mapper for converting vector DTOs and views into {@link PostRecord}.
 *
 * <p>This mapper centralizes transformations between:
 * DTO objects, entity models, and API resource representations.</p>
 *
 * <h2>Mapping Rules</h2>
 * <ul>
 *     <li>Several persistence-only fields are ignored</li>
 *     <li>{@code userName} is always initialized with {@code root}</li>
 *     <li>Unsupported enum values are mapped to {@code null}</li>
 * </ul>
 */
@Mapper(componentModel = "cdi")
public interface VectorRecordMapper extends DateTimeMapper {

    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "json", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "markdown", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(EntityModelResourceRecordView recordView);

    /**
     * Converts an update DTO into a {@link PostRecord} entity.
     *
     * @param record update DTO
     * @return mapped entity
     */
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "lastChangedTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "json", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "markdown", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(UpdateVectorRecord record);

    /**
     * Converts a {@link PostRecord} entity into a resource DTO.
     *
     * @param postRecord source entity
     * @return mapped resource DTO
     */
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    ResourceVectorRecord toResource(PostRecord postRecord);
}
