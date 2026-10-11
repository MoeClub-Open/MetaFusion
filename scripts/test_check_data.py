import unittest

import check_data


class ProductIdentifierChecks(unittest.TestCase):
    def test_known_isbn_and_gtin_vectors(self):
        for raw in ("0-306-40615-2", "0 8044 2957 x", "9780306406157", "9791090636071"):
            self.assertTrue(check_data.valid_isbn(check_data.compact_book_identifier(raw)), raw)
        for raw in ("0306406153", "X80442957X", "9780306406152", "ISBN 9780306406157"):
            self.assertFalse(check_data.valid_isbn(check_data.compact_book_identifier(raw)), raw)
        for raw in ("96385074", "036000291452", "4006381333931", "10012345000017"):
            self.assertTrue(check_data.valid_gtin(raw), raw)
            wrong = raw[:-1] + str((int(raw[-1]) + 1) % 10)
            self.assertFalse(check_data.valid_gtin(wrong), wrong)

    def test_audit_reports_invalid_declared_identifiers_as_p1(self):
        problems = []
        check_data.check_product_identifiers({
            "id": "release",
            "attributes": {"isbn": "9780306406152", "barcode": "96385075"},
            "external_ids": {"openbd": "0306406153", "isbndb": "0804429570", "ean_jan": "4006381333932"},
        }, problems)
        self.assertEqual(5, len(problems))
        self.assertTrue(all(item[0] == "P1" for item in problems))
        self.assertEqual(3, sum(item[1] == "invalid_isbn" for item in problems))
        self.assertEqual(2, sum(item[1] == "invalid_barcode" for item in problems))

    def test_audit_accepts_valid_identifiers_and_proprietary_barcodes(self):
        problems = []
        check_data.check_product_identifiers({
            "id": "release",
            "attributes": {"isbn": "0-8044-2957-X", "barcode": "REAL-1", "custom_code": "9780306406152"},
            "external_ids": {"openbd": "9780306406157", "ean_jan": "036000291452", "new_authority": "9780306406152"},
        }, problems)
        self.assertEqual([], problems)
        check_data.check_product_identifiers({"id": "empty"}, problems)
        self.assertEqual([], problems)

    def test_isbn_field_rejects_non_book_gtin(self):
        problems = []
        check_data.check_product_identifiers({"id": "release", "attributes": {"isbn": "4006381333931"}}, problems)
        self.assertEqual("invalid_isbn", problems[0][1])


if __name__ == "__main__":
    unittest.main()
