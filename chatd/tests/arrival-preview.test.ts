import { describe, expect, test } from "bun:test";
import { arrivalPreviewOf } from "../src/personal/arrival-preview.ts";
import type { BuzzEvent } from "../src/adapters/buzz/types.ts";

const photoDigest = "a".repeat(64);
const secondPhotoDigest = "b".repeat(64);

function sentWith(attachmentTags: string[][]): BuzzEvent {
	return {
		id: "m-1",
		pubkey: "이샘플",
		created_at: 1_800_000_000,
		kind: 9,
		tags: [["h", "dm-1"], ...attachmentTags],
		content: "박예시님 자료 보냅니다",
		sig: "",
	};
}

function picturedBy(event: BuzzEvent) {
	const { pictureDigest, pictureContentType } = arrivalPreviewOf(event);
	return { pictureDigest, pictureContentType };
}

describe("naming the first picture of an arriving message", () => {
	test("a picture is named by its digest and content type", () => {
		const event = sentWith([["imeta", "url https://relay.example.com/media/aaaa.jpg", "m image/jpeg", `x ${photoDigest}`]]);

		expect(picturedBy(event)).toEqual({ pictureDigest: photoDigest, pictureContentType: "image/jpeg" });
	});

	test("a message carrying only a video names no picture", () => {
		const event = sentWith([["imeta", "url https://relay.example.com/media/bbbb.mp4", "m video/mp4", `x ${photoDigest}`]]);

		expect(picturedBy(event)).toEqual({ pictureDigest: "", pictureContentType: "" });
	});

	test("a file before a picture leaves the picture named", () => {
		const event = sentWith([
			["imeta", "url https://relay.example.com/media/cccc.pdf", "m application/pdf", `x ${secondPhotoDigest}`],
			["imeta", "url https://relay.example.com/media/aaaa.png", "m image/png", `x ${photoDigest}`],
		]);

		expect(picturedBy(event)).toEqual({ pictureDigest: photoDigest, pictureContentType: "image/png" });
	});

	test("of two pictures the first is named", () => {
		const event = sentWith([
			["imeta", "url https://relay.example.com/media/aaaa.jpg", "m image/jpeg", `x ${photoDigest}`],
			["imeta", "url https://relay.example.com/media/bbbb.png", "m image/png", `x ${secondPhotoDigest}`],
		]);

		expect(picturedBy(event)).toEqual({ pictureDigest: photoDigest, pictureContentType: "image/jpeg" });
	});

	test("a picture without a digest names no picture", () => {
		const event = sentWith([["imeta", "url https://relay.example.com/media/aaaa.jpg", "m image/jpeg"]]);

		expect(picturedBy(event)).toEqual({ pictureDigest: "", pictureContentType: "" });
	});

	test("a message without attachments names no picture and keeps its words", () => {
		const event = sentWith([]);

		expect(arrivalPreviewOf(event)).toEqual({
			preview: "박예시님 자료 보냅니다",
			attachmentKinds: [],
			pictureDigest: "",
			pictureContentType: "",
		});
	});
});
