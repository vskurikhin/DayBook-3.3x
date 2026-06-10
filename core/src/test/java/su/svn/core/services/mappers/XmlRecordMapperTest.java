package su.svn.core.services.mappers;

import org.assertj.core.util.Lists;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.mapstruct.factory.Mappers;
import su.svn.core.domain.entities.BaseRecord;
import su.svn.core.domain.entities.Tag;
import su.svn.core.domain.entities.XmlRecord;
import su.svn.core.models.dto.NewXmlRecord;
import su.svn.core.models.dto.ResourceXmlRecord;
import su.svn.core.models.dto.UpdateXmlRecord;

import java.time.OffsetDateTime;
import java.util.Set;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;

class XmlRecordMapperTest {

    private XmlRecordMapper mapper;

    @BeforeEach
    void setUp() {
        mapper = Mappers.getMapper(XmlRecordMapper.class);
    }

    @Test
    void shouldMapEntityToResource() {
        UUID id = UUID.randomUUID();
        UUID parentId = UUID.randomUUID();

        BaseRecord baseRecord = BaseRecord.builder()
                .id(id)
                .parentId(parentId)
                .postAt(OffsetDateTime.now())
                .refreshAt(OffsetDateTime.now())
                .tags(Lists.list(Tag.builder().tag("xml").build()))
                .title("title")
                .build();

        XmlRecord entity = XmlRecord.builder()
                .id(id)
                .baseRecord(baseRecord)
                .xml("<root/>")
                .userName("user")
                .visible(true)
                .flags(1)
                .build();

        ResourceXmlRecord resource = mapper.toResource(entity);

        assertNotNull(resource);
        assertEquals(id, resource.id());
        assertEquals(parentId, resource.parentId());
        assertEquals("title", resource.title());
        assertEquals("<root/>", resource.xml());
        assertTrue(resource.visible());
        assertEquals(1, resource.flags());
        assertEquals(Set.of("xml"), resource.tags());
    }

    @Test
    void shouldMapNewRecordToResource() {
        OffsetDateTime postAt = OffsetDateTime.now();

        NewXmlRecord dto = NewXmlRecord.builder()
                .parentId(UUID.randomUUID())
                .title("new")
                .xml("<root/>")
                .postAt(postAt)
                .visible(true)
                .flags(2)
                .tags(Set.of("tag"))
                .build();

        ResourceXmlRecord resource = mapper.toResource(dto);

        assertNotNull(resource);
        assertEquals(dto.parentId(), resource.parentId());
        assertEquals(dto.title(), resource.title());
        assertEquals(dto.xml(), resource.xml());
        assertEquals(dto.postAt(), resource.postAt());
        assertEquals(dto.visible(), resource.visible());
        assertEquals(dto.flags(), resource.flags());
    }

    @Test
    void shouldMapUpdateRecordToResource() {
        UUID id = UUID.randomUUID();

        UpdateXmlRecord dto = UpdateXmlRecord.builder()
                .id(id)
                .parentId(UUID.randomUUID())
                .title("updated")
                .xml("<updated/>")
                .postAt(OffsetDateTime.now())
                .refreshAt(OffsetDateTime.now())
                .visible(true)
                .flags(3)
                .tags(Set.of("updated"))
                .build();

        ResourceXmlRecord resource = mapper.toResource(dto);

        assertNotNull(resource);
        assertEquals(dto.id(), resource.id());
        assertEquals(dto.parentId(), resource.parentId());
        assertEquals(dto.title(), resource.title());
        assertEquals(dto.xml(), resource.xml());
        assertEquals(dto.flags(), resource.flags());
    }

    @Test
    void shouldMapResourceToEntity() {
        UUID id = UUID.randomUUID();
        UUID parentId = UUID.randomUUID();

        ResourceXmlRecord resource = ResourceXmlRecord.builder()
                .id(id)
                .parentId(parentId)
                .title("resource")
                .xml("<root/>")
                .postAt(OffsetDateTime.now())
                .refreshAt(OffsetDateTime.now())
                .visible(true)
                .flags(7)
                .tags(Set.of("xml"))
                .build();

        XmlRecord entity = mapper.toEntity(resource);

        assertNotNull(entity);
        assertEquals(resource.id(), entity.id());
        assertEquals(resource.title(), entity.baseRecord().title());
        assertEquals(resource.xml(), entity.xml());
        assertTrue(entity.visible());
        assertEquals(resource.flags(), entity.flags());

        assertNotNull(entity.baseRecord());
        assertEquals(resource.id(), entity.baseRecord().id());
        assertEquals(resource.parentId(), entity.baseRecord().parentId());
        assertEquals(resource.postAt(), entity.baseRecord().postAt());
        assertEquals(resource.refreshAt(), entity.baseRecord().refreshAt());
        assertArrayEquals(resource.tags().toArray(), entity.baseRecord().tags().stream().map(Tag::tag).toArray());
    }
}